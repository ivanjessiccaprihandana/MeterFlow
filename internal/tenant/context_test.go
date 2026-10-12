package tenant

import "testing"

func TestRBAC(t *testing.T) {
	tests := []struct {
		role, scope string
		want        bool
	}{{"owner", "keys:write", true}, {"admin", "admin:audit", true}, {"billing", "billing:write", true}, {"billing", "keys:write", false}, {"member", "billing:read", true}, {"member", "usage:write", false}}
	for _, tt := range tests {
		if got := (Principal{Role: tt.role}).Can(tt.scope); got != tt.want {
			t.Errorf("role %s scope %s = %v want %v", tt.role, tt.scope, got, tt.want)
		}
	}
	if !(Principal{APIKeyID: "key", Scopes: map[string]bool{"usage:write": true}}).Can("usage:write") {
		t.Fatal("scoped API key denied")
	}
	if (Principal{APIKeyID: "key", Scopes: map[string]bool{"usage:write": true}}).Can("billing:write") {
		t.Fatal("API key escaped scope")
	}
}
