package observability

import (
	"fmt"
	"io"
	"sync/atomic"
)

type Metrics struct {
	requests  atomic.Uint64
	errors    atomic.Uint64
	upstream  atomic.Uint64
	active    atomic.Int64
	streaming atomic.Int64
}

func (m *Metrics) Request()  { m.requests.Add(1) }
func (m *Metrics) Error()    { m.errors.Add(1) }
func (m *Metrics) Upstream() { m.upstream.Add(1) }
func (m *Metrics) Begin(stream bool) func() {
	m.active.Add(1)
	if stream {
		m.streaming.Add(1)
	}
	return func() {
		m.active.Add(-1)
		if stream {
			m.streaming.Add(-1)
		}
	}
}
func (m *Metrics) Write(w io.Writer) {
	_, _ = fmt.Fprintf(w, "# HELP relayedock_requests_total Total gateway requests.\n# TYPE relayedock_requests_total counter\nrelayedock_requests_total %d\n# HELP relayedock_errors_total Total failed requests.\n# TYPE relayedock_errors_total counter\nrelayedock_errors_total %d\n# HELP relayedock_upstream_requests_total Total upstream requests.\n# TYPE relayedock_upstream_requests_total counter\nrelayedock_upstream_requests_total %d\n# HELP relayedock_active_requests Current active requests.\n# TYPE relayedock_active_requests gauge\nrelayedock_active_requests %d\n# HELP relayedock_streaming_requests Current streaming requests.\n# TYPE relayedock_streaming_requests gauge\nrelayedock_streaming_requests %d\n", m.requests.Load(), m.errors.Load(), m.upstream.Load(), m.active.Load(), m.streaming.Load())
}
