package server

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"canal/pkg/auth"
	"canal/pkg/protocol"

	"github.com/google/uuid"
)

type TunnelBinding struct {
	TunnelID   string
	ClientID   string
	Type       string
	PublicPort int
	LocalAddr  string
	Listener   net.Listener
	BasicAuth  *protocol.BasicAuthConfig
}

type PublicListenerManager struct {
	server      *Server
	mu          sync.Mutex
	nextHTTPP   int
	endHTTPPort int
}

func NewPublicListenerManager(srv *Server, startPort, endPort int) *PublicListenerManager {
	return &PublicListenerManager{
		server:      srv,
		nextHTTPP:   startPort,
		endHTTPPort: endPort,
	}
}

func (m *PublicListenerManager) CreateHTTPListener(clientID string, tunnelDef protocol.TunnelDef) (*TunnelBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	port := m.nextHTTPP
	m.nextHTTPP++
	if port > m.endHTTPPort {
		return nil, fmt.Errorf("no available ports in range")
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	binding := &TunnelBinding{
		TunnelID:   tunnelDef.ID,
		ClientID:   clientID,
		Type:       "http",
		PublicPort: port,
		LocalAddr:  tunnelDef.LocalAddr,
		Listener:   ln,
	}

	if tunnelDef.BasicAuth != nil {
		binding.BasicAuth = &protocol.BasicAuthConfig{
			Username: tunnelDef.BasicAuth.Username,
			Password: tunnelDef.BasicAuth.Password,
		}
	}

	slog.Info("started HTTP tunnel listener",
		"tunnel_id", tunnelDef.ID,
		"port", port,
		"local_addr", tunnelDef.LocalAddr,
		"auth", auth.HasBasicAuth((*auth.BasicAuthConfig)(binding.BasicAuth)),
	)

	go m.serveHTTPListener(ln, binding)
	return binding, nil
}

func (m *PublicListenerManager) serveHTTPListener(ln net.Listener, binding *TunnelBinding) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-m.server.stopCh:
				return
			default:
				slog.Error("accept error", "tunnel_id", binding.TunnelID, "error", err)
				return
			}
		}
		go m.handleHTTPConn(conn, binding)
	}
}

func (m *PublicListenerManager) handleHTTPConn(conn net.Conn, binding *TunnelBinding) {
	defer conn.Close()
	start := time.Now()

	client, ok := m.server.clients.Get(binding.ClientID)
	if !ok {
		slog.Warn("client not found for tunnel", "tunnel_id", binding.TunnelID)
		return
	}

	br := bufio.NewReaderSize(newReadWriteCloser(conn), 1<<20)
	req, err := http.ReadRequest(br)
	if err != nil {
		slog.Debug("failed to read HTTP request", "error", err)
		return
	}
	defer req.Body.Close()

	// Check Basic Auth if configured
	if binding.BasicAuth != nil {
		ba := &auth.BasicAuthConfig{
			Username: binding.BasicAuth.Username,
			Password: binding.BasicAuth.Password,
		}
		if !auth.CheckBasicAuth(ba, req.Header.Get("Authorization")) {
			slog.Debug("basic auth rejected", "path", req.URL.Path)
			writeHTTPResponse(conn, 401, "Unauthorized", map[string]string{
				"WWW-Authenticate": `Basic realm="tunnel"`,
			}, nil)
			return
		}
	}

	headers := make(map[string]string)
	for k, vals := range req.Header {
		headers[k] = vals[0]
	}

	body, _ := ioReadAll(req.Body)

	m.server.metrics.RequestsTotal.Add(1)

	reqPayload := protocol.HTTPRequestPayload{
		Method:  req.Method,
		URL:     req.URL.RequestURI(),
		Headers: headers,
		Body:    body,
	}

	streamID := uuid.New().String()
	payloadBytes := mustMarshal(reqPayload)

	msg := protocol.Message{
		Type:     protocol.MsgTypeHTTPRequest,
		StreamID: streamID,
		TunnelID: binding.TunnelID,
		Payload:  payloadBytes,
	}

	if err := client.Send(&msg); err != nil {
		slog.Error("failed to send HTTP request to client", "error", err)
		return
	}

	respChan := make(chan *protocol.Message, 1)
	m.server.pendingRespMu.Lock()
	m.server.pendingResponses[streamID] = respChan
	m.server.pendingRespMu.Unlock()

	defer func() {
		m.server.pendingRespMu.Lock()
		delete(m.server.pendingResponses, streamID)
		m.server.pendingRespMu.Unlock()
	}()

	var statusCode int
	var respBytes int64

	select {
	case respMsg := <-respChan:
		var respPayload protocol.HTTPResponsePayload
		if err := jsonUnmarshal(respMsg.Payload, &respPayload); err != nil {
			slog.Error("failed to unmarshal HTTP response", "error", err)
			writeHTTPResponse(conn, 502, "Bad Gateway", nil, nil)
			statusCode = 502
			break
		}

		statusCode = respPayload.StatusCode
		respBytes = int64(len(respPayload.Body))
		writeHTTPResponse(conn, respPayload.StatusCode, respPayload.StatusText, respPayload.Headers, respPayload.Body)

	case <-m.server.stopCh:
		writeHTTPResponse(conn, 504, "Gateway Timeout", nil, nil)
		statusCode = 504
	}

	reqBytes := int64(len(body))
	m.server.metrics.BytesSent.Add(respBytes)
	m.server.metrics.BytesReceived.Add(reqBytes)

	m.server.metrics.AddRecord(RequestRecord{
		StreamID:   streamID,
		TunnelID:   binding.TunnelID,
		Type:       "http",
		Method:     req.Method,
		Path:       req.URL.RequestURI(),
		StatusCode: statusCode,
		BytesSent:  respBytes,
		BytesRecv:  reqBytes,
		DurationMs: time.Since(start).Milliseconds(),
	})
}

func writeHTTPResponse(conn net.Conn, statusCode int, statusText string, headers map[string]string, body []byte) {
	resp := &http.Response{
		StatusCode: statusCode,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
	}
	resp.Header.Set("Content-Type", "text/plain")
	for k, v := range headers {
		resp.Header.Set(k, v)
	}
	if body != nil {
		resp.Body = ioNopCloserBytes(body)
	} else {
		resp.Body = ioNopCloserBytes([]byte(statusText))
	}
	resp.Write(conn)
}
