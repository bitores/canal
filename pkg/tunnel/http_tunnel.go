package tunnel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"canal/pkg/protocol"
)

func HandleHTTPRequest(conn WriteCloser, msg *protocol.Message, localAddr string) {
	var reqPayload protocol.HTTPRequestPayload
	if err := json.Unmarshal(msg.Payload, &reqPayload); err != nil {
		slog.Error("failed to unmarshal HTTP request", "error", err)
		sendError(conn, msg.StreamID, msg.TunnelID, "invalid request payload")
		return
	}

	url := fmt.Sprintf("http://%s%s", localAddr, reqPayload.URL)
	bodyReader := bytes.NewReader(reqPayload.Body)

	localReq, err := http.NewRequest(reqPayload.Method, url, bodyReader)
	if err != nil {
		slog.Error("failed to create local request", "error", err)
		sendError(conn, msg.StreamID, msg.TunnelID, err.Error())
		return
	}

	for k, v := range reqPayload.Headers {
		localReq.Header.Set(k, v)
	}

	client := &http.Client{}
	localResp, err := client.Do(localReq)
	if err != nil {
		slog.Error("failed to reach local service", "error", err)
		sendError(conn, msg.StreamID, msg.TunnelID, fmt.Sprintf("local service error: %v", err))
		return
	}
	defer localResp.Body.Close()

	body, err := io.ReadAll(localResp.Body)
	if err != nil {
		slog.Error("failed to read local response body", "error", err)
		sendError(conn, msg.StreamID, msg.TunnelID, err.Error())
		return
	}

	headers := make(map[string]string)
	for k, vals := range localResp.Header {
		if len(vals) > 0 {
			headers[k] = vals[0]
		}
	}

	respPayload := protocol.HTTPResponsePayload{
		StatusCode: localResp.StatusCode,
		StatusText: localResp.Status,
		Headers:    headers,
		Body:       body,
	}

	respMsg := protocol.Message{
		Type:     protocol.MsgTypeHTTPResponse,
		StreamID: msg.StreamID,
		TunnelID: msg.TunnelID,
		Payload:  mustMarshalPayload(respPayload),
	}

	data, err := protocol.Marshal(&respMsg)
	if err != nil {
		slog.Error("failed to marshal response", "error", err)
		return
	}

	if err := conn.WriteMessage(1, data); err != nil {
		slog.Error("failed to send response", "error", err)
	}
}

func sendError(conn WriteCloser, streamID, tunnelID, errMsg string) {
	errPayload := map[string]string{"error": errMsg}
	msg := protocol.Message{
		Type:     protocol.MsgTypeTunnelError,
		StreamID: streamID,
		TunnelID: tunnelID,
		Payload:  mustMarshalPayload(errPayload),
	}
	data, _ := protocol.Marshal(&msg)
	conn.WriteMessage(1, data)
}

func mustMarshalPayload(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

type WriteCloser interface {
	WriteMessage(int, []byte) error
	Close() error
}
