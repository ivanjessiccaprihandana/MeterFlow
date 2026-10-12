package auth

import (
	"context"
	"errors"
	"time"

	"github.com/example/meterflow/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var ErrUnauthorized = errors.New("invalid credentials")

type Store struct {
	DB                    *pgxpool.Pool
	Secret                string
	AccessTTL, RefreshTTL time.Duration
}
type Session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (s Store) Login(ctx context.Context, email, password, tenant string) (Session, error) {
	var uid, hash, tid, role string
	err := s.DB.QueryRow(ctx, `SELECT u.id,u.password_hash,m.tenant_id,m.role FROM users u JOIN memberships m ON m.user_id=u.id JOIN organizations o ON o.id=m.tenant_id WHERE lower(u.email)=lower($1) AND o.slug=$2`, email, tenant).Scan(&uid, &hash, &tid, &role)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return Session{}, ErrUnauthorized
	}
	return s.issue(ctx, uid, tid, role, platform.ID())
}
func (s Store) issue(ctx context.Context, uid, tid, role, family string) (Session, error) {
	now := time.Now().UTC()
	access, err := Sign(s.Secret, Claims{Subject: uid, TenantID: tid, Role: role, IssuedAt: now.Unix(), ExpiresAt: now.Add(s.AccessTTL).Unix()})
	if err != nil {
		return Session{}, err
	}
	refresh, err := RandomToken(48)
	if err != nil {
		return Session{}, err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO refresh_tokens(id,user_id,family_id,token_hash,expires_at) VALUES($1,$2,$3,$4,$5)`, platform.ID(), uid, family, Hash(refresh), now.Add(s.RefreshTTL))
	if err != nil {
		return Session{}, err
	}
	return Session{access, refresh, "Bearer", int64(s.AccessTTL.Seconds())}, nil
}
func (s Store) Refresh(ctx context.Context, token, tenant string) (Session, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback(ctx)
	var id, uid, family, tid, role string
	var expires time.Time
	var used, revoked *time.Time
	err = tx.QueryRow(ctx, `SELECT r.id,r.user_id,r.family_id,r.expires_at,r.used_at,r.revoked_at,m.tenant_id,m.role FROM refresh_tokens r JOIN memberships m ON m.user_id=r.user_id JOIN organizations o ON o.id=m.tenant_id WHERE r.token_hash=$1 AND o.slug=$2 FOR UPDATE`, Hash(token), tenant).Scan(&id, &uid, &family, &expires, &used, &revoked, &tid, &role)
	if err != nil || revoked != nil || time.Now().After(expires) {
		return Session{}, ErrUnauthorized
	}
	if used != nil {
		_, _ = tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=now() WHERE family_id=$1`, family)
		_ = tx.Commit(ctx)
		return Session{}, ErrUnauthorized
	}
	if _, err = tx.Exec(ctx, `UPDATE refresh_tokens SET used_at=now() WHERE id=$1`, id); err != nil {
		return Session{}, err
	}
	// Commit consumption before issue; a failure forces login instead of allowing replay.
	if err = tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return s.issue(ctx, uid, tid, role, family)
}

func HashPassword(v string) (string, error) {
	b, e := bcrypt.GenerateFromPassword([]byte(v), bcrypt.DefaultCost)
	return string(b), e
}
