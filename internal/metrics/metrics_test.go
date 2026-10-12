package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCountsServerErrors(t *testing.T) {
	m := New()
	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if m.Requests.Load() != 1 || m.Errors.Load() != 1 {
		t.Fatalf("requests=%d errors=%d", m.Requests.Load(), m.Errors.Load())
	}
}
