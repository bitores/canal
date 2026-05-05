package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"canal/pkg/config"
	"canal/pkg/server"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func init() {
	_ = version
	_ = commit
	_ = date
}

func main() {
	cfgPath := flag.String("config", "", "config file path")
	listenAddr := flag.String("addr", ":7000", "websocket listen address")
	publicHost := flag.String("host", "localhost", "public hostname")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file")
	tlsKey := flag.String("tls-key", "", "TLS private key file")
	dashboardAddr := flag.String("dashboard-addr", ":8080", "dashboard listen address")
	tokenFile := flag.String("token-file", "", "token authentication file (YAML)")
	proxyAddr := flag.String("proxy-addr", ":8081", "subdomain proxy listen address (empty to disable)")
	userFile := flag.String("user-file", "", "user accounts file path (enables dashboard auth)")
	adminUser := flag.String("admin-user", "", "admin user email")
	adminPass := flag.String("admin-pass", "", "admin user password")
	flag.Parse()

	cfg := config.DefaultServerConfig()
	if *cfgPath != "" {
		slog.Warn("config file loading not implemented", "path", *cfgPath)
	}
	if *listenAddr != ":7000" {
		cfg.ListenAddr = *listenAddr
	}
	if *publicHost != "localhost" {
		cfg.PublicHost = *publicHost
	}
	if *tlsCert != "" {
		cfg.TLSCertFile = *tlsCert
	}
	if *tlsKey != "" {
		cfg.TLSKeyFile = *tlsKey
	}
	if *tokenFile != "" {
		cfg.TokenFile = *tokenFile
	}
	if *dashboardAddr != ":8080" {
		cfg.DashboardAddr = *dashboardAddr
	}
	if *proxyAddr != ":8081" {
		cfg.ProxyAddr = *proxyAddr
	}
	if *userFile != "" {
		cfg.UserFile = *userFile
	}
	if *adminUser != "" {
		cfg.AdminUser = *adminUser
	}
	if *adminPass != "" {
		cfg.AdminPass = *adminPass
	}

	srv, err := server.NewServer(cfg)
	if err != nil {
		slog.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	if err := srv.Start(); err != nil {
		slog.Error("failed to start server", "error", err)
		os.Exit(1)
	}

	slog.Info("server started", "addr", cfg.ListenAddr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("shutting down...")
	_ = srv.Stop()
}
