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
	"strings"
	"time"

	"canal/pkg/auth"
)

//go:embed static/*
var dashboardStatic embed.FS

type contextKey string

const ctxKeyEmail contextKey = "email"

type DashboardServer struct {
	addr         string
	server       *Server
	metrics      *MetricsCollector
	httpSrv      *http.Server
	authRequired bool
}

func NewDashboardServer(addr string, srv *Server) *DashboardServer {
	return &DashboardServer{
		addr:         addr,
		server:       srv,
		metrics:      srv.metrics,
		authRequired: srv.config.UserFile != "",
	}
}

func (d *DashboardServer) Start() error {
	mux := http.NewServeMux()

	// Public routes (no auth required)
	mux.HandleFunc("/api/register", d.handleRegister)
	mux.HandleFunc("/api/login", d.handleLogin)
	mux.HandleFunc("/api/session/check", d.handleSessionCheck)

	// Protected routes
	mux.HandleFunc("/api/status", d.handleStatus)
	mux.HandleFunc("/api/clients", d.handleClients)
	mux.HandleFunc("/api/tunnels", d.handleTunnels)
	mux.HandleFunc("/api/requests", d.handleRequests)
	mux.HandleFunc("/api/metrics", d.handleMetrics)
	mux.HandleFunc("/api/tokens", d.handleTokens)
	mux.HandleFunc("/api/logout", d.handleLogout)
	mux.HandleFunc("/api/admin/users", d.handleAdminUsers)
	mux.HandleFunc("/api/admin/users/", d.handleAdminDeleteUser)

	staticFS, err := fs.Sub(dashboardStatic, "static")
	if err != nil {
		slog.Warn("no embedded dashboard static files", "error", err)
	} else {
		mux.Handle("/", http.FileServer(http.FS(staticFS)))
	}

	d.httpSrv = &http.Server{
		Addr:    d.addr,
		Handler: d.authMiddleware(mux),
	}

	slog.Info("dashboard starting", "addr", d.addr)
	go func() {
		if err := d.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("dashboard error", "error", err)
		}
	}()
	return nil
}

func (d *DashboardServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Public routes and static files: skip auth
		if path == "/api/register" || path == "/api/login" || path == "/api/session/check" || !strings.HasPrefix(path, "/api") {
			next.ServeHTTP(w, r)
			return
		}

		// If auth not required, allow all
		if !d.authRequired {
			next.ServeHTTP(w, r)
			return
		}

		// Extract and validate Bearer token
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeJSON(w, map[string]string{"error": "unauthorized"})
			return
		}
		token := auth[7:]
		email, ok := d.server.sessionStore.Validate(token)
		if !ok {
			writeJSON(w, map[string]string{"error": "unauthorized"})
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeyEmail, email)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
			entry := map[string]any{
				"tunnel_id":    tb.TunnelID,
				"client_id":    tb.ClientID,
				"type":         tb.Type,
				"local_addr":   tb.LocalAddr,
				"public_url":   fmt.Sprintf("%s://%s:%d", scheme, d.server.config.PublicHost, tb.PublicPort),
				"port":         tb.PublicPort,
				"has_auth":     tb.BasicAuth != nil,
			}
			if tb.Subdomain != "" && d.server.config.ProxyAddr != "" {
				entry["subdomain_url"] = formatSubdomainURL(
					d.server.config.PublicHost,
					proxyPortFromAddr(d.server.config.ProxyAddr),
					tb.Subdomain, scheme)
			}
			result = append(result, entry)
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

func (d *DashboardServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	if !d.authRequired {
		writeJSON(w, map[string]string{"error": "user authentication is not configured on server"})
		return
	}

	var req struct {
		Email           string `json:"email"`
		Password        string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Email == "" || req.Password == "" {
		writeJSON(w, map[string]string{"error": "email and password are required"})
		return
	}
	if len(req.Password) < 6 {
		writeJSON(w, map[string]string{"error": "password must be at least 6 characters"})
		return
	}

	if err := d.server.userStore.CreateUser(req.Email, req.Password); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	slog.Info("user registered", "email", req.Email)
	writeJSON(w, map[string]string{"message": "user created successfully"})
}

func (d *DashboardServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "invalid request body"})
		return
	}

	if d.authRequired {
		if !d.server.userStore.ValidatePassword(req.Email, req.Password) {
			writeJSON(w, map[string]string{"error": "invalid email or password"})
			return
		}
	} else {
		// No auth configured: allow any login for development
		if req.Email == "" {
			writeJSON(w, map[string]string{"error": "email is required"})
			return
		}
	}

	sessionToken := d.server.sessionStore.Create(req.Email)
	slog.Info("user logged in", "email", req.Email)
	writeJSON(w, map[string]string{"session_token": sessionToken, "email": req.Email})
}

func (d *DashboardServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	if !d.authRequired {
		writeJSON(w, map[string]string{"message": "ok"})
		return
	}

	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		d.server.sessionStore.Revoke(auth[7:])
	}
	writeJSON(w, map[string]string{"message": "logged out"})
}

func (d *DashboardServer) handleSessionCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}

	if !d.authRequired {
		writeJSON(w, map[string]any{"authenticated": true, "email": "", "is_admin": false})
		return
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		writeJSON(w, map[string]any{"authenticated": false})
		return
	}

	email, ok := d.server.sessionStore.Validate(auth[7:])
	if !ok {
		writeJSON(w, map[string]any{"authenticated": false})
		return
	}

	isAdmin := d.server.userStore.IsAdmin(email)
	writeJSON(w, map[string]any{"authenticated": true, "email": email, "is_admin": isAdmin})
}

func (d *DashboardServer) handleTokens(w http.ResponseWriter, r *http.Request) {
	email, _ := r.Context().Value(ctxKeyEmail).(string)

	switch r.Method {
	case "GET":
		if d.authRequired && !d.server.userStore.IsAdmin(email) {
			var userTokens []auth.TokenEntry
			for _, t := range d.server.TokenStore().List() {
				if t.Label == email {
					userTokens = append(userTokens, t)
				}
			}
			writeJSON(w, userTokens)
		} else {
			entries := d.server.TokenStore().List()
			writeJSON(w, entries)
		}

	case "POST":
		if d.authRequired && email == "" {
			writeJSON(w, map[string]string{"error": "unauthorized"})
			return
		}

		label := email
		if !d.authRequired {
			var req struct {
				Label string `json:"label"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, map[string]string{"error": "invalid request body"})
				return
			}
			label = req.Label
			if label == "" {
				writeJSON(w, map[string]string{"error": "label is required"})
				return
			}
		}

		token, err := d.server.TokenStore().Generate(label)
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		if d.authRequired {
			d.server.userStore.AddToken(email, token)
		}

		slog.Info("token generated", "label", label)
		writeJSON(w, map[string]string{"token": token, "label": label})

	default:
		http.Error(w, `{"error":"method not allowed"}`, 405)
	}
}

func (d *DashboardServer) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}

	email, _ := r.Context().Value(ctxKeyEmail).(string)
	if !d.server.userStore.IsAdmin(email) {
		writeJSON(w, map[string]string{"error": "forbidden"})
		return
	}

	users := d.server.userStore.ListUsers()
	type userInfo struct {
		Email     string   `json:"email"`
		Tokens    []string `json:"tokens"`
		CreatedAt string   `json:"created_at"`
		IsAdmin   bool     `json:"is_admin"`
	}
	result := make([]userInfo, 0, len(users))
	for _, u := range users {
		result = append(result, userInfo{
			Email:     u.Email,
			Tokens:    u.Tokens,
			CreatedAt: u.CreatedAt,
			IsAdmin:   u.IsAdmin,
		})
	}
	writeJSON(w, result)
}

func (d *DashboardServer) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}

	adminEmail, _ := r.Context().Value(ctxKeyEmail).(string)
	if !d.server.userStore.IsAdmin(adminEmail) {
		writeJSON(w, map[string]string{"error": "forbidden"})
		return
	}

	targetEmail := r.URL.Path[len("/api/admin/users/"):]
	if targetEmail == "" {
		writeJSON(w, map[string]string{"error": "email is required"})
		return
	}

	// Remove all tokens owned by this user
	user := d.server.userStore.ListUsers()
	for _, u := range user {
		if u.Email == targetEmail {
			for _, tok := range u.Tokens {
				d.server.TokenStore().Remove(tok)
			}
			break
		}
	}

	if err := d.server.userStore.DeleteUser(targetEmail); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	slog.Info("admin deleted user", "admin", adminEmail, "target", targetEmail)
	writeJSON(w, map[string]string{"message": "user deleted"})
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
	_ = json.NewEncoder(w).Encode(v)
}
