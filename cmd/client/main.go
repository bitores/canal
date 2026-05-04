package main

import (
	"crypto/tls"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"canal/pkg/client"
	"canal/pkg/config"

	"github.com/gorilla/websocket"
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
	serverAddr := flag.String("server", "ws://localhost:7000", "server websocket address")
	tunnelFlag := flag.String("tunnel", "", "tunnel in format 'type:localaddr' e.g. 'http:localhost:3000' or 'tcp:localhost:22'")
	authToken := flag.String("token", "", "auth token")
	insecure := flag.Bool("insecure", false, "skip TLS certificate verification")
	flag.Parse()

	cfg := config.DefaultClientConfig()
	cfg.ServerAddr = *serverAddr
	cfg.AuthToken = *authToken

	if *insecure {
		websocket.DefaultDialer.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
		slog.Warn("TLS certificate verification disabled")
	}

	if *tunnelFlag != "" {
		tunType := "http"
		localAddr := *tunnelFlag

		if idx := strings.IndexByte(*tunnelFlag, ':'); idx > 0 {
			prefix := (*tunnelFlag)[:idx]
			if prefix == "http" || prefix == "tcp" {
				tunType = prefix
				localAddr = (*tunnelFlag)[idx+1:]
			}
		}

		cfg.Tunnels = []config.TunnelCfg{
			{
				ID:        "tun-1",
				Type:      tunType,
				LocalAddr: localAddr,
			},
		}
	}

	cli := client.NewClient(cfg)
	if err := cli.Start(); err != nil {
		slog.Error("failed to start client", "error", err)
		os.Exit(1)
	}

	slog.Info("client started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("shutting down...")
	_ = cli.Stop()
}
