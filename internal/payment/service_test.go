package payment

import (
	"testing"
	"time"
)

func TestWebhookSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"id":"evt_1"}`)
	ts, sig := Sign("secret", now, body)
	s := Service{Secret: "secret"}
	if err := s.Verify(ts, sig, body, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ts, sig, []byte("tampered"), now); err == nil {
		t.Fatal("tampered body accepted")
	}
	if err := s.Verify(ts, sig, body, now.Add(6*time.Minute)); err == nil {
		t.Fatal("stale webhook accepted")
	}
}
