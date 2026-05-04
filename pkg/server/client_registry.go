package server

import (
	"encoding/base64"
	"log/slog"
	"net"
	"sync"

	"canal/pkg/protocol"
)

type ClientSession struct {
	ID          string
	Conn        WSConn
	Token       string
	TokenLabel  string
	Tunnels     map[string]*TunnelBinding
	ConnectedAt string

	writeMu       sync.Mutex
	activeStreams map[string]*tcpStream
	streamsMu     sync.Mutex
}

type tcpStream struct {
	streamID string
	tunnelID string
	conn     net.Conn
	closeCh  chan struct{}
}

func (s *ClientSession) Send(msg *protocol.Message) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	data, err := protocol.Marshal(msg)
	if err != nil {
		return err
	}
	return s.Conn.WriteMessage(1, data)
}

func (s *ClientSession) addStream(streamID string, stream *tcpStream) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.activeStreams == nil {
		s.activeStreams = make(map[string]*tcpStream)
	}
	s.activeStreams[streamID] = stream
}

func (s *ClientSession) removeStream(streamID string) *tcpStream {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.activeStreams == nil {
		return nil
	}
	stream := s.activeStreams[streamID]
	delete(s.activeStreams, streamID)
	return stream
}

func (s *ClientSession) getStream(streamID string) *tcpStream {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.activeStreams == nil {
		return nil
	}
	return s.activeStreams[streamID]
}

func (s *ClientSession) closeAllStreams() {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	for id, stream := range s.activeStreams {
		_ = stream.conn.Close()
		close(stream.closeCh)
		delete(s.activeStreams, id)
	}
}

type ClientRegistry struct {
	clients map[string]*ClientSession
	mu      sync.RWMutex
}

func NewClientRegistry() *ClientRegistry {
	return &ClientRegistry{
		clients: make(map[string]*ClientSession),
	}
}

func (r *ClientRegistry) Add(session *ClientSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[session.ID] = session
	slog.Info("client registered", "id", session.ID, "label", session.TokenLabel)
}

func (r *ClientRegistry) Remove(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if session, ok := r.clients[clientID]; ok {
		session.closeAllStreams()
		for _, tb := range session.Tunnels {
			if tb.Listener != nil {
				_ = tb.Listener.Close()
			}
		}
		delete(r.clients, clientID)
		slog.Info("client disconnected", "id", clientID)
	}
}

func (r *ClientRegistry) Get(clientID string) (*ClientSession, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.clients[clientID]
	return s, ok
}

func (r *ClientRegistry) List() []*ClientSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ClientSession, 0, len(r.clients))
	for _, s := range r.clients {
		result = append(result, s)
	}
	return result
}

func (r *ClientRegistry) FindByTunnelID(tunnelID string) (*ClientSession, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.clients {
		if _, ok := s.Tunnels[tunnelID]; ok {
			return s, true
		}
	}
	return nil, false
}

func (r *ClientRegistry) FindByPublicPort(port int) (*ClientSession, *TunnelBinding, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.clients {
		for _, tb := range s.Tunnels {
			if tb.PublicPort == port {
				return s, tb, true
			}
		}
	}
	return nil, nil, false
}

type WSConn interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(int, []byte) error
	Close() error
}

func writeTunnelData(conn WSConn, writeMu *sync.Mutex, streamID, tunnelID string, data []byte) error {
	encoded := base64.StdEncoding.EncodeToString(data)
	payload := map[string]string{"data": encoded}

	msg := protocol.Message{
		Type:     protocol.MsgTypeTunnelData,
		StreamID: streamID,
		TunnelID: tunnelID,
		Payload:  mustMarshal(payload),
	}

	writeMu.Lock()
	defer writeMu.Unlock()
	msgData, err := protocol.Marshal(&msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(1, msgData)
}

func writeTunnelClose(conn WSConn, writeMu *sync.Mutex, streamID, tunnelID string) error {
	msg := protocol.Message{
		Type:     protocol.MsgTypeTunnelClose,
		StreamID: streamID,
		TunnelID: tunnelID,
	}

	writeMu.Lock()
	defer writeMu.Unlock()
	msgData, err := protocol.Marshal(&msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(1, msgData)
}

type dataPayload struct {
	Data string `json:"data"`
}

func unmarshalDataPayload(raw []byte) ([]byte, error) {
	var p dataPayload
	if err := jsonUnmarshal(raw, &p); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(p.Data)
}

