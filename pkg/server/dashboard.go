package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

//go:embed static/*
var dashboardStatic embed.FS

type DashboardServer struct {
	addr    string
	server  *Server
	metrics *MetricsCollector
	httpSrv *http.Server
}

func NewDashboardServer(addr string, srv *Server) *DashboardServer {
	return &DashboardServer{
		addr:    addr,
		server:  srv,
		metrics: srv.metrics,
	}
}

func (d *DashboardServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", d.handleStatus)
	mux.HandleFunc("/api/clients", d.handleClients)
	mux.HandleFunc("/api/tunnels", d.handleTunnels)
	mux.HandleFunc("/api/requests", d.handleRequests)
	mux.HandleFunc("/api/metrics", d.handleMetrics)

	staticFS, err := fs.Sub(dashboardStatic, "static")
	if err != nil {
		slog.Warn("no embedded dashboard static files", "error", err)
	} else {
		mux.Handle("/", http.FileServer(http.FS(staticFS)))
	}

	d.httpSrv = &http.Server{
		Addr:    d.addr,
		Handler: mux,
	}

	slog.Info("dashboard starting", "addr", d.addr)
	go func() {
		if err := d.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("dashboard error", "error", err)
		}
	}()
	return nil
}

func (d *DashboardServer) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return d.httpSrv.Shutdown(ctx)
}

func (d *DashboardServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	clients := d.server.clients.List()
	activeTunnels := 0
	for _, c := range clients {
		activeTunnels += len(c.Tunnels)
	}

	resp := map[string]any{
		"uptime":         d.metrics.Uptime(),
		"clients":        len(clients),
		"tunnels":        activeTunnels,
		"requests_total": d.metrics.RequestsTotal.Load(),
		"bytes_sent":     d.metrics.BytesSent.Load() + d.metrics.TCPBytesSent.Load(),
		"bytes_received": d.metrics.BytesReceived.Load() + d.metrics.TCPBytesRecv.Load(),
		"active_streams": d.metrics.ActiveStreams.Load(),
	}
	writeJSON(w, resp)
}

func (d *DashboardServer) handleClients(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/clients"):]
	if len(id) > 1 {
		id = id[1:]
	}

	if id != "" {
		session, ok := d.server.clients.Get(id)
		if !ok {
			http.Error(w, "client not found", 404)
			return
		}
		writeJSON(w, clientToMap(session))
		return
	}

	clients := d.server.clients.List()
	result := make([]map[string]any, 0, len(clients))
	for _, c := range clients {
		result = append(result, clientToMap(c))
	}
	writeJSON(w, result)
}

func (d *DashboardServer) handleTunnels(w http.ResponseWriter, r *http.Request) {
	clients := d.server.clients.List()
	result := make([]map[string]any, 0)

	for _, c := range clients {
		for _, tb := range c.Tunnels {
			scheme := "http"
			if tb.Type == "tcp" {
				scheme = "tcp"
			}
			result = append(result, map[string]any{
				"tunnel_id":  tb.TunnelID,
				"client_id":  tb.ClientID,
				"type":       tb.Type,
				"local_addr": tb.LocalAddr,
				"public_url": fmt.Sprintf("%s://%s:%d", scheme, d.server.config.PublicHost, tb.PublicPort),
				"port":       tb.PublicPort,
				"has_auth":   tb.BasicAuth != nil,
			})
		}
	}
	writeJSON(w, result)
}

func (d *DashboardServer) handleRequests(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}
	history := d.metrics.GetHistory(limit)
	writeJSON(w, history)
}

func (d *DashboardServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"requests_total": d.metrics.RequestsTotal.Load(),
		"bytes_sent":     d.metrics.BytesSent.Load(),
		"bytes_received": d.metrics.BytesReceived.Load(),
		"active_streams": d.metrics.ActiveStreams.Load(),
		"tcp_bytes_sent": d.metrics.TCPBytesSent.Load(),
		"tcp_bytes_recv": d.metrics.TCPBytesRecv.Load(),
		"uptime":         d.metrics.Uptime(),
	}
	writeJSON(w, resp)
}

func clientToMap(c *ClientSession) map[string]any {
	tunnels := make([]map[string]any, 0, len(c.Tunnels))
	for _, tb := range c.Tunnels {
		tunnels = append(tunnels, map[string]any{
			"tunnel_id":   tb.TunnelID,
			"type":        tb.Type,
			"local_addr":  tb.LocalAddr,
			"public_port": tb.PublicPort,
		})
	}
	return map[string]any{
		"id":           c.ID,
		"token_label":  c.TokenLabel,
		"connected_at": c.ConnectedAt,
		"tunnels":      tunnels,
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
