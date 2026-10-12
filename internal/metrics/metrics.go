package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Metrics struct {
	Requests, Errors atomic.Uint64
	Started          time.Time
}

func New() *Metrics { return &Metrics{Started: time.Now()} }

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }
func (m *Metrics) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Requests.Add(1)
		rw := &recorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		if rw.status >= 500 {
			m.Errors.Add(1)
		}
	})
}
func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# TYPE meterflow_http_requests_total counter\nmeterflow_http_requests_total %d\n# TYPE meterflow_http_errors_total counter\nmeterflow_http_errors_total %d\n# TYPE meterflow_uptime_seconds gauge\nmeterflow_uptime_seconds %.0f\n", m.Requests.Load(), m.Errors.Load(), time.Since(m.Started).Seconds())
}
