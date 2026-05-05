package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"canal/pkg/config"
	"canal/pkg/protocol"
	"canal/pkg/tunnel"

	"github.com/gorilla/websocket"
)

type Client struct {
	config    *config.ClientConfig
	conn      *websocket.Conn
	tunnels   []protocol.TunnelDef
	sessionID string

	writeMu        sync.Mutex
	activeStreams  map[string]*localStream
	streamsMu      sync.Mutex
	stopCh         chan struct{}
	stopOnce       sync.Once
}

type localStream struct {
	streamID string
	tunnelID string
	conn     net.Conn
	closeCh  chan struct{}
}

func NewClient(cfg *config.ClientConfig) *Client {
	return &Client{
		config:        cfg,
		stopCh:        make(chan struct{}),
		activeStreams: make(map[string]*localStream),
	}
}

func (c *Client) Start() error {
	tunnels := make([]protocol.TunnelDef, len(c.config.Tunnels))
	for i, t := range c.config.Tunnels {
		tunnels[i] = protocol.TunnelDef{
			ID:          t.ID,
			Type:        t.Type,
			LocalAddr:   t.LocalAddr,
			RequestHost: t.RequestHost,
			RemotePort:  t.RemotePort,
		}
	}
	c.tunnels = tunnels

	if err := c.connect(); err != nil {
		return err
	}
	return nil
}

func (c *Client) Stop() error {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	if c.conn != nil {
		_ = c.conn.Close()
	}
	return nil
}

func (c *Client) connect() error {
	wsURL := c.config.ServerAddr + "/tunnel"
	slog.Info("connecting to server", "addr", wsURL)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return err
	}
	c.conn = conn

	if err := c.register(); err != nil {
		_ = conn.Close()
		return err
	}

	slog.Info("connected and registered", "session", c.sessionID)
	go c.readLoop()
	go c.heartbeatLoop()

	return nil
}

func (c *Client) register() error {
	regPayload := protocol.RegisterPayload{
		Token:   c.config.AuthToken,
		Tunnels: c.tunnels,
	}

	msg := protocol.Message{
		Type:    protocol.MsgTypeRegister,
		Payload: mustMarshalClient(regPayload),
	}

	data, err := protocol.Marshal(&msg)
	if err != nil {
		return err
	}

	if err := c.conn.WriteMessage(1, data); err != nil {
		return err
	}

	_, ackData, err := c.conn.ReadMessage()
	if err != nil {
		return err
	}

	var ackMsg protocol.Message
	if err := protocol.Unmarshal(ackData, &ackMsg); err != nil {
		return err
	}

	var ack protocol.RegisterAckPayload
	if err := json.Unmarshal(ackMsg.Payload, &ack); err != nil {
		return err
	}

	if !ack.Success {
		errMsg := ack.Error
		if errMsg == "" {
			errMsg = "registration rejected by server"
		}
		return fmt.Errorf("registration failed: %s", errMsg)
	}

	for _, t := range ack.Tunnels {
		if t.Error != "" {
			slog.Warn("tunnel error", "id", t.ID, "error", t.Error)
		} else {
			slog.Info("tunnel active", "id", t.ID, "url", t.PublicURL)
			if t.SubdomainURL != "" {
				slog.Info("tunnel subdomain", "id", t.ID, "url", t.SubdomainURL)
			}
		}
	}

	return nil
}

func (c *Client) readLoop() {
	for {
		_, msgData, err := c.conn.ReadMessage()
		if err != nil {
			slog.Info("connection closed", "error", err)
			c.reconnect()
			return
		}

		var msg protocol.Message
		if err := protocol.Unmarshal(msgData, &msg); err != nil {
			slog.Warn("invalid message", "error", err)
			continue
		}

		switch msg.Type {
		case protocol.MsgTypeHeartbeat:
			ack := protocol.Message{Type: protocol.MsgTypeHeartbeatAck}
		_ = c.sendWSMessage(&ack)

		case protocol.MsgTypeHeartbeatAck:
			// heartbeat acknowledged

		case protocol.MsgTypeHTTPRequest:
			go c.handleHTTPRequest(&msg)

		case protocol.MsgTypeTunnelOpen:
			c.handleTunnelOpen(&msg)

		case protocol.MsgTypeTunnelData:
			c.handleTunnelData(&msg)

		case protocol.MsgTypeTunnelClose:
			c.handleTunnelClose(&msg)

		default:
			slog.Debug("unhandled message type", "type", msg.Type)
		}
	}
}

func (c *Client) sendWSMessage(msg *protocol.Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	data, err := protocol.Marshal(msg)
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(1, data)
}

func (c *Client) handleHTTPRequest(msg *protocol.Message) {
	localAddr := c.getLocalAddr(msg.TunnelID)
	if localAddr == "" {
		slog.Warn("unknown tunnel", "id", msg.TunnelID)
		return
	}

	tunnel.HandleHTTPRequest(&lockedConn{conn: c.conn, mu: &c.writeMu}, msg, localAddr)
}

func (c *Client) getLocalAddr(tunnelID string) string {
	for _, t := range c.config.Tunnels {
		if t.ID == tunnelID {
			return t.LocalAddr
		}
	}
	return ""
}

func (c *Client) addStream(streamID string, ls *localStream) {
	c.streamsMu.Lock()
	defer c.streamsMu.Unlock()
	c.activeStreams[streamID] = ls
}

func (c *Client) removeStream(streamID string) *localStream {
	c.streamsMu.Lock()
	defer c.streamsMu.Unlock()
	ls := c.activeStreams[streamID]
	delete(c.activeStreams, streamID)
	return ls
}

func (c *Client) getStream(streamID string) *localStream {
	c.streamsMu.Lock()
	defer c.streamsMu.Unlock()
	return c.activeStreams[streamID]
}

func (c *Client) handleTunnelOpen(msg *protocol.Message) {
	var openPayload struct {
		LocalAddr string `json:"local_addr"`
	}
	if err := json.Unmarshal(msg.Payload, &openPayload); err != nil {
		slog.Error("invalid tunnel_open payload", "error", err)
		return
	}

	localAddr := openPayload.LocalAddr
	if localAddr == "" {
		localAddr = c.getLocalAddr(msg.TunnelID)
	}
	if localAddr == "" {
		slog.Warn("tunnel_open for unknown tunnel", "id", msg.TunnelID)
		return
	}

	conn, err := net.DialTimeout("tcp", localAddr, 10*time.Second)
	if err != nil {
		slog.Error("failed to connect to local service", "addr", localAddr, "error", err)
		errPayload := map[string]string{"error": err.Error()}
		errMsg := protocol.Message{
			Type:     protocol.MsgTypeTunnelError,
			StreamID: msg.StreamID,
			TunnelID: msg.TunnelID,
			Payload:  mustMarshalClient(errPayload),
		}
		_ = c.sendWSMessage(&errMsg)
		return
	}

	ls := &localStream{
		streamID: msg.StreamID,
		tunnelID: msg.TunnelID,
		conn:     conn,
		closeCh:  make(chan struct{}),
	}
	c.addStream(msg.StreamID, ls)

	slog.Debug("local TCP connection established",
		"stream_id", msg.StreamID,
		"local_addr", localAddr)

	go c.localReadPump(conn, msg.StreamID, msg.TunnelID, ls)
}

func (c *Client) handleTunnelData(msg *protocol.Message) {
	ls := c.getStream(msg.StreamID)
	if ls == nil {
		slog.Warn("tunnel_data for unknown stream", "stream_id", msg.StreamID)
		return
	}

	data, err := decodeDataPayload(msg.Payload)
	if err != nil {
		slog.Error("failed to decode tunnel_data", "error", err)
		return
	}

	if _, err := ls.conn.Write(data); err != nil {
		slog.Debug("local TCP write error", "stream_id", msg.StreamID, "error", err)
		if removed := c.removeStream(msg.StreamID); removed != nil {
		_ = removed.conn.Close()
			close(removed.closeCh)
		}
	}
}

func (c *Client) handleTunnelClose(msg *protocol.Message) {
	if ls := c.removeStream(msg.StreamID); ls != nil {
		_ = ls.conn.Close()
		close(ls.closeCh)
		slog.Debug("local TCP stream closed", "stream_id", msg.StreamID)
	}
}

func (c *Client) localReadPump(conn net.Conn, streamID, tunnelID string, ls *localStream) {
	defer func() {
		c.removeStream(streamID)
		_ = conn.Close()
	}()

	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ls.closeCh:
			return
		default:
		}

		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		encoded := base64.StdEncoding.EncodeToString(data)
		payload := map[string]string{"data": encoded}
		msg := protocol.Message{
			Type:     protocol.MsgTypeTunnelData,
			StreamID: streamID,
			TunnelID: tunnelID,
			Payload:  mustMarshalClient(payload),
		}

		if err := c.sendWSMessage(&msg); err != nil {
			slog.Error("failed to send tunnel_data", "error", err)
			return
		}
	}
}

func (c *Client) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			msg := protocol.Message{Type: protocol.MsgTypeHeartbeat}
			_ = c.sendWSMessage(&msg)

		case <-c.stopCh:
			return
		}
	}
}

func (c *Client) reconnect() {
	backoff := 1 * time.Second
	maxBackoff := 60 * time.Second

	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		slog.Info("reconnecting in", "delay", backoff)
		time.Sleep(backoff)

		if err := c.connect(); err != nil {
			slog.Warn("reconnect failed", "error", err)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		return
	}
}

func mustMarshalClient(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// lockedConn wraps a WebSocket connection with a mutex for concurrent writes.
type lockedConn struct {
	conn *websocket.Conn
	mu   sync.Locker
}

func (l *lockedConn) WriteMessage(msgType int, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.conn.WriteMessage(msgType, data)
}

func (l *lockedConn) Close() error {
	return l.conn.Close()
}

type dataPayload struct {
	Data string `json:"data"`
}

func decodeDataPayload(raw json.RawMessage) ([]byte, error) {
	var p dataPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(p.Data)
}
