package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] [<type> <addr> ...]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Tunnel definitions (positional args):\n")
		fmt.Fprintf(os.Stderr, "  http <port|addr>    HTTP tunnel to local service\n")
		fmt.Fprintf(os.Stderr, "  tcp <port|addr>     TCP tunnel to local service\n")
		fmt.Fprintf(os.Stderr, "  <port>              shorthand for 'http <port>'\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s http 3000\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s tcp 22\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s 3000\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s http 3000 tcp 22\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nFlags:\n")
		flag.PrintDefaults()
	}
	serverAddr := flag.String("server", "ws://localhost:7000", "server websocket address")
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

	// Parse positional arguments as tunnel definitions:
	//   canal-client http 3000          -> http tunnel to localhost:3000
	//   canal-client tcp 22             -> tcp tunnel to localhost:22
	//   canal-client 3000               -> http tunnel to localhost:3000 (default type)
	//   canal-client http 3000 tcp 22   -> multiple tunnels
	//   canal-client                    -> no tunnels (YAML config only)
	if args := flag.Args(); len(args) > 0 {
		var tunnels []config.TunnelCfg
		tunnelID := 0
		for i := 0; i < len(args); i++ {
			tunType := "http"
			addr := args[i]

			if args[i] == "http" || args[i] == "tcp" {
				tunType = args[i]
				i++
				if i >= len(args) {
					slog.Error("missing address after tunnel type", "type", tunType)
					os.Exit(1)
				}
				addr = args[i]
			}

			if isPurePort(addr) {
				addr = "localhost:" + addr
			}

			tunnelID++
			tunnels = append(tunnels, config.TunnelCfg{
				ID:        fmt.Sprintf("tun-%d", tunnelID),
				Type:      tunType,
				LocalAddr: addr,
			})
		}
		cfg.Tunnels = tunnels
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

// isPurePort checks if s is a bare port number (all digits).
func isPurePort(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
