package auth

import (
	"testing"
	"time"
)

func TestTokenRoundTripAndExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	want := Claims{Subject: "u", TenantID: "t", Role: "owner", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix()}
	token, err := Sign("01234567890123456789012345678901", want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify("01234567890123456789012345678901", token, now)
	if err != nil || got.Subject != want.Subject {
		t.Fatalf("got %#v err %v", got, err)
	}
	if _, err = Verify("01234567890123456789012345678901", token, now.Add(2*time.Minute)); err == nil {
		t.Fatal("expired token accepted")
	}
}
