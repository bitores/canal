package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"canal/pkg/auth"
	"canal/pkg/protocol"
)

type SubdomainRegistry struct {
	mu           sync.RWMutex
	subdomainMap map[string]*TunnelBinding
}

func NewSubdomainRegistry() *SubdomainRegistry {
	return &SubdomainRegistry{
		subdomainMap: make(map[string]*TunnelBinding),
	}
}

func (r *SubdomainRegistry) Register(subdomain string, binding *TunnelBinding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subdomainMap[subdomain] = binding
}

func (r *SubdomainRegistry) Unregister(subdomain string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subdomainMap, subdomain)
}

func (r *SubdomainRegistry) Lookup(subdomain string) *TunnelBinding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.subdomainMap[subdomain]
}

func (r *SubdomainRegistry) GenerateRandom() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return "tun-" + hex.EncodeToString(buf)
}

type SubdomainProxy struct {
	registry *SubdomainRegistry
	server   *Server
}

func NewSubdomainProxy(srv *Server) *SubdomainProxy {
	return &SubdomainProxy{
		registry: srv.subdomainRegistry,
		server:   srv,
	}
}

func (p *SubdomainProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}

	subdomain := host
	if idx := strings.Index(host, "."); idx >= 0 {
		subdomain = host[:idx]
	}

	if subdomain == "" {
		http.NotFound(w, r)
		return
	}

	binding := p.registry.Lookup(subdomain)
	if binding == nil {
		http.NotFound(w, r)
		return
	}

	p.handleRequest(w, r, binding)
}

func (p *SubdomainProxy) handleRequest(w http.ResponseWriter, r *http.Request, binding *TunnelBinding) {
	start := time.Now()

	client, ok := p.server.clients.Get(binding.ClientID)
	if !ok {
		slog.Warn("client not found for subdomain tunnel", "tunnel_id", binding.TunnelID)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	if binding.BasicAuth != nil {
		ba := &auth.BasicAuthConfig{
			Username: binding.BasicAuth.Username,
			Password: binding.BasicAuth.Password,
		}
		if !auth.CheckBasicAuth(ba, r.Header.Get("Authorization")) {
			slog.Debug("basic auth rejected by subdomain proxy", "path", r.URL.Path)
			w.Header().Set("WWW-Authenticate", `Basic realm="tunnel"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	headers := make(map[string]string)
	for k, vals := range r.Header {
		headers[k] = vals[0]
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Debug("failed to read request body", "error", err)
		body = nil
	}

	_ = client // used implicitly via sendHTTPRequest
	p.server.metrics.RequestsTotal.Add(1)

	reqPayload := protocol.HTTPRequestPayload{
		Method:  r.Method,
		URL:     r.URL.RequestURI(),
		Headers: headers,
		Body:    body,
	}

	streamID, respMsg, err := p.server.sendHTTPRequest(binding, &reqPayload)

	var statusCode int
	var respBytes int64

	if err != nil {
		slog.Warn("subdomain proxy request failed",
			"tunnel_id", binding.TunnelID,
			"error", err)
		http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
		statusCode = 504
	} else {
		var respPayload protocol.HTTPResponsePayload
		if err := json.Unmarshal(respMsg.Payload, &respPayload); err != nil {
			slog.Error("failed to unmarshal HTTP response in subdomain proxy", "error", err)
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
			statusCode = 502
		} else {
			statusCode = respPayload.StatusCode
			respBytes = int64(len(respPayload.Body))

			for k, v := range respPayload.Headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(respPayload.StatusCode)
			if len(respPayload.Body) > 0 {
				_, _ = w.Write(respPayload.Body)
			} else {
				_, _ = w.Write([]byte(respPayload.StatusText))
			}
		}
	}

	reqBytes := int64(len(body))
	p.server.metrics.BytesSent.Add(respBytes)
	p.server.metrics.BytesReceived.Add(reqBytes)
	p.server.metrics.AddRecord(RequestRecord{
		StreamID:   streamID,
		TunnelID:   binding.TunnelID,
		Type:       "http",
		Method:     r.Method,
		Path:       r.URL.RequestURI(),
		StatusCode: statusCode,
		BytesSent:  respBytes,
		BytesRecv:  reqBytes,
		DurationMs: time.Since(start).Milliseconds(),
	})
}
