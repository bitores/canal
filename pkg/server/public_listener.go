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
)

type TunnelBinding struct {
	TunnelID   string
	ClientID   string
	Type       string
	PublicPort int
	LocalAddr  string
	Listener   net.Listener
	BasicAuth  *protocol.BasicAuthConfig
	Subdomain  string
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
	defer func() { _ = conn.Close() }()
	start := time.Now()

	br := bufio.NewReaderSize(newReadWriteCloser(conn), 1<<20)
	req, err := http.ReadRequest(br)
	if err != nil {
		slog.Warn("failed to read HTTP request", "error", err)
		return
	}
	defer func() { _ = req.Body.Close() }()

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

	body, err := ioReadAll(req.Body)
	if err != nil {
		slog.Debug("failed to read request body", "error", err)
		body = nil
	}

	m.server.metrics.RequestsTotal.Add(1)

	reqPayload := protocol.HTTPRequestPayload{
		Method:  req.Method,
		URL:     req.URL.RequestURI(),
		Headers: headers,
		Body:    body,
	}

	streamID, respMsg, err := m.server.sendHTTPRequest(binding, &reqPayload)

	var statusCode int
	var respBytes int64

	if err != nil {
		slog.Warn("HTTP request failed",
			"tunnel_id", binding.TunnelID,
			"error", err)
		writeHTTPResponse(conn, 504, "Gateway Timeout", nil, nil)
		statusCode = 504
	} else {
		var respPayload protocol.HTTPResponsePayload
		if err := jsonUnmarshal(respMsg.Payload, &respPayload); err != nil {
			slog.Error("failed to unmarshal HTTP response", "error", err)
			writeHTTPResponse(conn, 502, "Bad Gateway", nil, nil)
			statusCode = 502
		} else {
			statusCode = respPayload.StatusCode
			respBytes = int64(len(respPayload.Body))
			writeHTTPResponse(conn, respPayload.StatusCode, respPayload.StatusText, respPayload.Headers, respPayload.Body)
		}
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
	_ = resp.Write(conn)
}
