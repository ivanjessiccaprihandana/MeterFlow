package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var raw = base64.RawURLEncoding

type Claims struct {
	Subject   string `json:"sub"`
	TenantID  string `json:"tid"`
	Role      string `json:"role"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
}

func Sign(secret string, c Claims) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	unsigned := raw.EncodeToString(header) + "." + raw.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + raw.EncodeToString(mac.Sum(nil)), nil
}
func Verify(secret, token string, now time.Time) (Claims, error) {
	var c Claims
	p := strings.Split(token, ".")
	if len(p) != 3 {
		return c, errors.New("malformed token")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(p[0] + "." + p[1]))
	sig, e := raw.DecodeString(p[2])
	if e != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return c, errors.New("invalid signature")
	}
	b, e := raw.DecodeString(p[1])
	if e != nil || json.Unmarshal(b, &c) != nil {
		return c, errors.New("invalid claims")
	}
	if c.ExpiresAt <= now.Unix() {
		return c, errors.New("token expired")
	}
	return c, nil
}
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return raw.EncodeToString(b), nil
}
func Hash(v string) []byte { x := sha256.Sum256([]byte(v)); return x[:] }
func APIKey() (plain, prefix string, hash []byte, err error) {
	s, e := RandomToken(32)
	if e != nil {
		return "", "", nil, e
	}
	plain = "mf_live_" + s
	prefix = plain[:16]
	return plain, prefix, Hash(plain), nil
}
func Bearer(v string) (string, error) {
	p := strings.SplitN(v, " ", 2)
	if len(p) != 2 || !strings.EqualFold(p[0], "Bearer") {
		return "", fmt.Errorf("bearer token required")
	}
	return p[1], nil
}
