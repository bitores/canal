package protocol

import "encoding/json"

type MessageType string

const (
	MsgTypeRegister     MessageType = "register"
	MsgTypeRegisterAck  MessageType = "register_ack"
	MsgTypeHeartbeat    MessageType = "heartbeat"
	MsgTypeHeartbeatAck MessageType = "heartbeat_ack"

	MsgTypeHTTPRequest  MessageType = "http_request"
	MsgTypeHTTPResponse MessageType = "http_response"

	MsgTypeTunnelOpen  MessageType = "tunnel_open"
	MsgTypeTunnelData  MessageType = "tunnel_data"
	MsgTypeTunnelClose MessageType = "tunnel_close"
	MsgTypeTunnelError MessageType = "tunnel_error"
)

type Message struct {
	Type     MessageType     `json:"type"`
	StreamID string          `json:"stream_id,omitempty"`
	TunnelID string          `json:"tunnel_id,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}
