package rtspserver

import (
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"strings"
	"time"

	gs "github.com/djwackey/dorsvr/groupsock"
	"github.com/djwackey/dorsvr/livemedia"
)

type RTSPServer struct {
	urlPrefix                         string
	rtspPort                          int
	httpPort                          int
	rtspListen                        *net.TCPListener
	httpListen                        *net.TCPListener
	clientSessions                    map[string]*RTSPClientSession
	clientConnectionsForHTTPTunneling map[string]*RTSPClientConnection
	serverMediaSessions               map[string]*livemedia.ServerMediaSession
	reclamationTestSeconds            time.Duration
}

func New() *RTSPServer {
	server := new(RTSPServer)

	runtime.GOMAXPROCS(runtime.NumCPU())

	server.clientSessions = make(map[string]*RTSPClientSession)
	server.serverMediaSessions = make(map[string]*livemedia.ServerMediaSession)
	server.clientConnectionsForHTTPTunneling = make(map[string]*RTSPClientConnection)
	server.reclamationTestSeconds = 65
	return server
}

func (s *RTSPServer) Listen(portNum int) bool {
	s.rtspPort = portNum

	var err error
	s.rtspListen, err = s.setupOurSocket(portNum)
	if err != nil {
		return false
	}

	s.startMonitor()
	return true
}

func (s *RTSPServer) Start() {
	go s.IncomingConnectionHandler(s.rtspListen)
}

func (s *RTSPServer) startMonitor() {
	go s.monitorServe()
}

func (s *RTSPServer) monitorServe() {
	log.Println(http.ListenAndServe("0.0.0.0:6060", nil))
}

func (s *RTSPServer) setupOurSocket(portNum int) (*net.TCPListener, error) {
	tcpAddr := fmt.Sprintf("0.0.0.0:%d", portNum)
	addr, _ := net.ResolveTCPAddr("tcp", tcpAddr)

	return net.ListenTCP("tcp", addr)
}

func (s *RTSPServer) SetUpTunnelingOverHTTP(httpPort int) bool {
	s.httpPort = httpPort

	var err error
	s.httpListen, err = s.setupOurSocket(httpPort)
	if err != nil {
		return false
	}

	go s.IncomingConnectionHandler(s.httpListen)
	return true
}

func (s *RTSPServer) HttpServerPortNum() int {
	return s.httpPort
}

func (s *RTSPServer) RtspURL(streamName string) string {
	urlPrefix := s.RtspURLPrefix()
	return fmt.Sprintf("%s%s", urlPrefix, streamName)
}

func (s *RTSPServer) RtspURLPrefix() string {
	s.urlPrefix, _ = gs.OurIPAddress()
	return fmt.Sprintf("rtsp://%s:%d/", s.urlPrefix, s.rtspPort)
}

func (s *RTSPServer) IncomingConnectionHandler(serverListen *net.TCPListener) {
	for {
		tcpConn, err := serverListen.AcceptTCP()
		if err != nil {
			fmt.Println("failed to accept client.")
			continue
		}

		tcpConn.SetReadBuffer(50 * 1024)

		// Create a new object for handling server RTSP connection:
		go s.NewClientConnection(tcpConn)
	}
}

func (s *RTSPServer) NewClientConnection(conn net.Conn) {
	connection := NewRTSPClientConnection(s, conn)
	if connection != nil {
		connection.IncomingRequestHandler()
	}
}

func (s *RTSPServer) LookupServerMediaSession(streamName string) *livemedia.ServerMediaSession {
	// Next, check whether we already have a "ServerMediaSession" for server file:
	sms, smsExists := s.serverMediaSessions[streamName]

	fid, err := os.Open(streamName)
	if err != nil {
		if smsExists {
			s.RemoveServerMediaSession(sms)
		}
		return nil
	}
	defer fid.Close()

	if !smsExists {
		sms = s.CreateNewSMS(streamName)
		s.AddServerMediaSession(sms)
	}

	return sms
}

func (s *RTSPServer) AddServerMediaSession(serverMediaSession *livemedia.ServerMediaSession) {
	sessionName := serverMediaSession.StreamName()

	// in case an existing "ServerMediaSession" with server name already exists
	session, _ := s.serverMediaSessions[sessionName]
	s.RemoveServerMediaSession(session)

	s.serverMediaSessions[sessionName] = serverMediaSession
}

func (s *RTSPServer) RemoveServerMediaSession(serverMediaSession *livemedia.ServerMediaSession) {
	if serverMediaSession != nil {
		sessionName := serverMediaSession.StreamName()
		delete(s.serverMediaSessions, sessionName)
	}
}

func (s *RTSPServer) CreateNewSMS(fileName string) *livemedia.ServerMediaSession {
	var serverMediaSession *livemedia.ServerMediaSession

	array := strings.Split(fileName, ".")
	if len(array) < 2 {
		return nil
	}

	extension := array[1]

	switch extension {
	case "264":
		// Assumed to be a H.264 Video Elementary Stream file:
		serverMediaSession = livemedia.NewServerMediaSession("H.264 Video", fileName)
		serverMediaSession.AddSubSession(livemedia.NewH264FileMediaSubSession(fileName))
	case "ts":
		//indexFileName := fmt.Sprintf("%sx", fileName)
		serverMediaSession = livemedia.NewServerMediaSession("MPEG Transport Stream", fileName)
		serverMediaSession.AddSubSession(livemedia.NewM2TSFileMediaSubSession(fileName))
	default:
	}
	return serverMediaSession
}
