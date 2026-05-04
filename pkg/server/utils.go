package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"time"
)

func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func jsonUnmarshal(data json.RawMessage, v any) error {
	return json.Unmarshal(data, v)
}

func ioReadAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

func ioNopCloserBytes(b []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(b))
}

type readWriteCloser struct {
	conn   net.Conn
	reader *io.LimitedReader
}

func newReadWriteCloser(conn net.Conn) *readWriteCloser {
	return &readWriteCloser{
		conn:   conn,
		reader: &io.LimitedReader{R: conn, N: int64(1 << 20)},
	}
}

func (r *readWriteCloser) Read(p []byte) (int, error) {
	_ = r.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	return r.reader.Read(p)
}

func (r *readWriteCloser) Write(p []byte) (int, error) {
	_ = r.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return r.conn.Write(p)
}

func (r *readWriteCloser) Close() error {
	return r.conn.Close()
}
