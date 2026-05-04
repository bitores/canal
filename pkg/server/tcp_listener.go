package server

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"canal/pkg/protocol"

	"github.com/google/uuid"
)

type TCPListenerManager struct {
	server      *Server
	mu          sync.Mutex
	nextTCPP    int
	endTCPPort  int
}

func NewTCPListenerManager(srv *Server, startPort, endPort int) *TCPListenerManager {
	return &TCPListenerManager{
		server:     srv,
		nextTCPP:   startPort,
		endTCPPort: endPort,
	}
}

func (m *TCPListenerManager) CreateTCPListener(clientID string, tunnelDef protocol.TunnelDef) (*TunnelBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	port := m.nextTCPP
	m.nextTCPP++
	if port > m.endTCPPort {
		return nil, fmt.Errorf("no available TCP ports in range")
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on TCP port %d: %w", port, err)
	}

	binding := &TunnelBinding{
		TunnelID:   tunnelDef.ID,
		ClientID:   clientID,
		Type:       "tcp",
		PublicPort: port,
		LocalAddr:  tunnelDef.LocalAddr,
		Listener:   ln,
	}

	slog.Info("started TCP tunnel listener",
		"tunnel_id", tunnelDef.ID,
		"port", port,
		"local_addr", tunnelDef.LocalAddr,
	)

	go m.serveTCPListener(ln, binding)
	return binding, nil
}

func (m *TCPListenerManager) serveTCPListener(ln net.Listener, binding *TunnelBinding) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-m.server.stopCh:
				return
			default:
				slog.Error("TCP accept error", "tunnel_id", binding.TunnelID, "error", err)
				return
			}
		}
		go m.handleTCPConn(conn, binding)
	}
}

func (m *TCPListenerManager) handleTCPConn(tcpConn net.Conn, binding *TunnelBinding) {
	client, ok := m.server.clients.Get(binding.ClientID)
	if !ok {
		slog.Warn("client not found for TCP tunnel", "tunnel_id", binding.TunnelID)
		tcpConn.Close()
		return
	}

	streamID := uuid.New().String()
	m.server.metrics.ActiveStreams.Add(1)

	stream := &tcpStream{
		streamID: streamID,
		tunnelID: binding.TunnelID,
		conn:     tcpConn,
		closeCh:  make(chan struct{}),
	}
	client.addStream(streamID, stream)

	openPayload := map[string]string{
		"stream_id":  streamID,
		"tunnel_id":  binding.TunnelID,
		"local_addr": binding.LocalAddr,
	}
	openMsg := protocol.Message{
		Type:     protocol.MsgTypeTunnelOpen,
		StreamID: streamID,
		TunnelID: binding.TunnelID,
		Payload:  mustMarshal(openPayload),
	}

	if err := client.Send(&openMsg); err != nil {
		slog.Error("failed to send tunnel_open", "error", err)
		client.removeStream(streamID)
		m.server.metrics.ActiveStreams.Add(-1)
		tcpConn.Close()
		return
	}

	slog.Debug("TCP stream opened",
		"stream_id", streamID,
		"tunnel_id", binding.TunnelID,
		"remote", tcpConn.RemoteAddr(),
	)

	m.readPump(tcpConn, client, stream, binding)
	m.server.metrics.ActiveStreams.Add(-1)
}

func (m *TCPListenerManager) readPump(tcpConn net.Conn, client *ClientSession, stream *tcpStream, binding *TunnelBinding) {
	var totalBytes int64
	defer func() {
		m.server.metrics.TCPBytesRecv.Add(totalBytes)
		writeTunnelClose(client.Conn, &client.writeMu, stream.streamID, binding.TunnelID)
		client.removeStream(stream.streamID)
		tcpConn.Close()
	}()

	buf := make([]byte, 32*1024)
	for {
		select {
		case <-stream.closeCh:
			return
		default:
		}

		n, err := tcpConn.Read(buf)
		if err != nil {
			if err != io.EOF {
				slog.Debug("TCP read error",
					"stream_id", stream.streamID,
					"error", err)
			}
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		totalBytes += int64(n)

		if err := writeTunnelData(client.Conn, &client.writeMu, stream.streamID, binding.TunnelID, data); err != nil {
			slog.Error("failed to send tunnel_data", "error", err)
			return
		}
	}
}

func (s *Server) handleTunnelData(client *ClientSession, msg *protocol.Message) {
	stream := client.getStream(msg.StreamID)
	if stream == nil {
		slog.Warn("tunnel_data for unknown stream", "stream_id", msg.StreamID)
		return
	}

	data, err := unmarshalDataPayload(msg.Payload)
	if err != nil {
		slog.Error("failed to decode tunnel_data payload", "error", err)
		return
	}

	if _, err := stream.conn.Write(data); err != nil {
		slog.Debug("TCP write error", "stream_id", msg.StreamID, "error", err)
		if removed := client.removeStream(msg.StreamID); removed != nil {
			removed.conn.Close()
			close(removed.closeCh)
		}
		return
	}
	s.metrics.TCPBytesSent.Add(int64(len(data)))
}
