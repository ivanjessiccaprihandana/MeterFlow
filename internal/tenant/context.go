package tenant

import "context"

type Principal struct {
	UserID, TenantID, Role, APIKeyID string
	Scopes                           map[string]bool
}
type key struct{}

func With(ctx context.Context, p Principal) context.Context { return context.WithValue(ctx, key{}, p) }
func From(ctx context.Context) (Principal, bool)            { p, ok := ctx.Value(key{}).(Principal); return p, ok }
func (p Principal) Can(scope string) bool {
	if p.APIKeyID != "" {
		return p.Scopes[scope]
	}
	switch p.Role {
	case "owner", "admin":
		return true
	case "billing":
		return scope == "billing:read" || scope == "billing:write" || scope == "subscription:read"
	case "member":
		return scope == "billing:read" || scope == "subscription:read"
	default:
		return false
	}
}
