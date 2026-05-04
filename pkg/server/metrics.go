package server

import (
	"sync"
	"sync/atomic"
	"time"
)

type MetricsCollector struct {
	RequestsTotal   atomic.Int64
	BytesSent       atomic.Int64
	BytesReceived   atomic.Int64
	ActiveStreams   atomic.Int64
	TCPBytesSent    atomic.Int64
	TCPBytesRecv    atomic.Int64

	mu       sync.Mutex
	history  []RequestRecord
	maxSize  int
	startedAt time.Time
}

type RequestRecord struct {
	StreamID   string `json:"stream_id"`
	TunnelID   string `json:"tunnel_id"`
	Type       string `json:"type"`
	RemoteAddr string `json:"remote_addr"`
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	BytesSent  int64  `json:"bytes_sent"`
	BytesRecv  int64  `json:"bytes_recv"`
	DurationMs int64  `json:"duration_ms"`
	Time       string `json:"time"`
	Error      string `json:"error,omitempty"`
}

func NewMetricsCollector(maxRecords int) *MetricsCollector {
	return &MetricsCollector{
		history:   make([]RequestRecord, 0, maxRecords),
		maxSize:   maxRecords,
		startedAt: time.Now(),
	}
}

func (m *MetricsCollector) AddRecord(rec RequestRecord) {
	rec.Time = time.Now().Format(time.RFC3339Nano)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history = append(m.history, rec)
	if len(m.history) > m.maxSize {
		m.history = m.history[len(m.history)-m.maxSize:]
	}
}

func (m *MetricsCollector) GetHistory(limit int) []RequestRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > len(m.history) {
		limit = len(m.history)
	}
	if limit > len(m.history) {
		return m.history
	}
	result := make([]RequestRecord, limit)
	copy(result, m.history[len(m.history)-limit:])
	return result
}

func (m *MetricsCollector) Uptime() string {
	return time.Since(m.startedAt).Truncate(time.Second).String()
}
