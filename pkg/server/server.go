package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"canal/pkg/auth"
	"canal/pkg/config"
	"canal/pkg/protocol"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Server struct {
	config           *config.ServerConfig
	clients          *ClientRegistry
	listenerMgr      *PublicListenerManager
	tcpListenerMgr   *TCPListenerManager
	pendingResponses map[string]chan *protocol.Message
	pendingRespMu    sync.Mutex
	stopCh           chan struct{}
	stopOnce         sync.Once
	httpServer       *http.Server
	tokenStore       *auth.TokenStore
	metrics          *MetricsCollector
	dashboard        *DashboardServer
}

func NewServer(cfg *config.ServerConfig) (*Server, error) {
	s := &Server{
		config:           cfg,
		clients:          NewClientRegistry(),
		pendingResponses: make(map[string]chan *protocol.Message),
		stopCh:           make(chan struct{}),
		tokenStore:       auth.NewTokenStore(),
	}

	s.listenerMgr = NewPublicListenerManager(s, 18080, 18180)
	s.tcpListenerMgr = NewTCPListenerManager(s, 19000, 19100)
	s.metrics = NewMetricsCollector(1000)

	if cfg.TokenFile != "" {
		if err := s.tokenStore.LoadFile(cfg.TokenFile); err != nil {
			slog.Warn("failed to load token file", "path", cfg.TokenFile, "error", err)
		} else {
			slog.Info("loaded tokens from file", "path", cfg.TokenFile)
		}
	}

	return s, nil
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","service":"canal"}`))
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/tunnel", s.handleTunnel)

	s.httpServer = &http.Server{
		Addr:    s.config.ListenAddr,
		Handler: mux,
	}

	if s.config.DashboardAddr != "" {
		s.dashboard = NewDashboardServer(s.config.DashboardAddr, s)
		if err := s.dashboard.Start(); err != nil {
			return err
		}
	}

	slog.Info("server starting", "addr", s.config.ListenAddr)
	go func() {
		if s.config.TLSCertFile != "" && s.config.TLSKeyFile != "" {
			slog.Info("TLS enabled", "cert", s.config.TLSCertFile)
			if err := s.httpServer.ListenAndServeTLS(s.config.TLSCertFile, s.config.TLSKeyFile); err != nil && err != http.ErrServerClosed {
				slog.Error("server error", "error", err)
			}
		} else {
			if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("server error", "error", err)
			}
		}
	}()

	return nil
}

func (s *Server) Stop() error {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	if s.dashboard != nil {
		s.dashboard.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}

	slog.Info("new websocket connection", "remote", r.RemoteAddr)

	_, msgData, err := conn.ReadMessage()
	if err != nil {
		slog.Error("failed to read initial message", "error", err)
		conn.Close()
		return
	}

	var msg protocol.Message
	if err := protocol.Unmarshal(msgData, &msg); err != nil {
		slog.Error("failed to unmarshal message", "error", err)
		conn.Close()
		return
	}

	if msg.Type != protocol.MsgTypeRegister {
		slog.Warn("expected register message, got", "type", msg.Type)
		sendRegisterError(conn, "expected register message")
		return
	}

	var regPayload protocol.RegisterPayload
	if err := json.Unmarshal(msg.Payload, &regPayload); err != nil {
		slog.Error("failed to unmarshal register payload", "error", err)
		sendRegisterError(conn, "invalid payload")
		return
	}

	// Validate token
	tokenLabel := ""
	if s.tokenStore.IsEnabled() {
		var ok bool
		tokenLabel, ok = s.tokenStore.Validate(regPayload.Token)
		if !ok {
			slog.Warn("invalid token", "token", regPayload.Token)
			sendRegisterError(conn, "invalid authentication token")
			conn.Close()
			return
		}
		slog.Info("token validated", "label", tokenLabel)
	} else {
		slog.Warn("no token configured, accepting all connections")
	}

	slog.Info("register request", "tunnels", len(regPayload.Tunnels))

	clientID := uuid.New().String()
	session := &ClientSession{
		ID:          clientID,
		Conn:        conn,
		Token:       regPayload.Token,
		TokenLabel:  tokenLabel,
		ConnectedAt: time.Now().Format(time.RFC3339),
		Tunnels:     make(map[string]*TunnelBinding),
	}

	var assignments []protocol.TunnelAssign
	for _, td := range regPayload.Tunnels {
		assign := protocol.TunnelAssign{
			ID: td.ID,
		}

		switch td.Type {
		case "http":
			binding, err := s.listenerMgr.CreateHTTPListener(clientID, td)
			if err != nil {
				assign.Error = err.Error()
			} else {
				binding.ClientID = clientID
				binding.BasicAuth = td.BasicAuth
				session.Tunnels[td.ID] = binding
				assign.PublicURL = formatPublicURL(s.config.PublicHost, binding.PublicPort, "http")
			}
		case "tcp":
			binding, err := s.tcpListenerMgr.CreateTCPListener(clientID, td)
			if err != nil {
				assign.Error = err.Error()
			} else {
				binding.ClientID = clientID
				session.Tunnels[td.ID] = binding
				assign.PublicURL = "tcp://" + s.config.PublicHost + ":" + strconv.Itoa(binding.PublicPort)
			}
		default:
			assign.Error = "unsupported tunnel type: " + td.Type
		}

		assignments = append(assignments, assign)
	}

	s.clients.Add(session)

	ackPayload := protocol.RegisterAckPayload{
		Success: true,
		Tunnels: assignments,
	}

	ackMsg := protocol.Message{
		Type:    protocol.MsgTypeRegisterAck,
		Payload: mustMarshal(ackPayload),
	}

	ackData, _ := protocol.Marshal(&ackMsg)
	if err := conn.WriteMessage(1, ackData); err != nil {
		slog.Error("failed to send register ack", "error", err)
		s.clients.Remove(clientID)
		conn.Close()
		return
	}

	go s.handleClientMessages(clientID, session)
}

func sendRegisterError(conn *websocket.Conn, errMsg string) {
	ack := protocol.RegisterAckPayload{
		Success: false,
		Error:   errMsg,
	}
	msg := protocol.Message{
		Type:    protocol.MsgTypeRegisterAck,
		Payload: mustMarshal(ack),
	}
	data, _ := protocol.Marshal(&msg)
	conn.WriteMessage(1, data)
	conn.Close()
}

func (s *Server) handleClientMessages(clientID string, session *ClientSession) {
	conn := session.Conn
	defer func() {
		s.clients.Remove(clientID)
		conn.Close()
	}()

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			slog.Info("client disconnected", "id", clientID, "error", err)
			return
		}

		var msg protocol.Message
		if err := protocol.Unmarshal(msgData, &msg); err != nil {
			slog.Warn("invalid message from client", "id", clientID, "error", err)
			continue
		}

		switch msg.Type {
		case protocol.MsgTypeHeartbeat:
			ack := protocol.Message{Type: protocol.MsgTypeHeartbeatAck}
			session.Send(&ack)

		case protocol.MsgTypeHTTPResponse:
			s.pendingRespMu.Lock()
			if ch, ok := s.pendingResponses[msg.StreamID]; ok {
				msgCopy := msg
				ch <- &msgCopy
			}
			s.pendingRespMu.Unlock()

		case protocol.MsgTypeTunnelData:
			s.handleTunnelData(session, &msg)

		default:
			slog.Debug("unhandled message type", "type", msg.Type)
		}
	}
}

func formatPublicURL(host string, port int, scheme string) string {
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + host + ":" + intToStr(port)
}

func intToStr(n int) string {
	return strconv.Itoa(n)
}
