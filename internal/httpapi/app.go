package httpapi

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/example/meterflow/internal/auth"
	"github.com/example/meterflow/internal/billing"
	"github.com/example/meterflow/internal/config"
	"github.com/example/meterflow/internal/metering"
	"github.com/example/meterflow/internal/metrics"
	"github.com/example/meterflow/internal/payment"
	"github.com/example/meterflow/internal/platform"
	"github.com/example/meterflow/internal/subscription"
	"github.com/example/meterflow/internal/tenant"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type App struct {
	cfg       config.Config
	db        *pgxpool.Pool
	redis     *redis.Client
	auth      auth.Store
	meter     metering.Service
	subs      subscription.Service
	bills     billing.Service
	pay       payment.Service
	metrics   *metrics.Metrics
	dashboard *template.Template
}

func New(cfg config.Config, db *pgxpool.Pool, rdb *redis.Client) *App {
	return &App{cfg: cfg, db: db, redis: rdb, auth: auth.Store{DB: db, Secret: cfg.JWTSecret, AccessTTL: cfg.AccessTTL, RefreshTTL: cfg.RefreshTTL}, meter: metering.Service{DB: db, Redis: rdb, Limit: cfg.RateLimitPerMinute}, subs: subscription.Service{DB: db}, bills: billing.Service{DB: db}, pay: payment.Service{DB: db, Secret: cfg.WebhookSecret}, metrics: metrics.New(), dashboard: template.Must(template.New("dashboard").Parse(dashboardHTML))}
}

func (a *App) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /health/ready", a.ready)
	m.Handle("GET /metrics", a.metrics)
	m.HandleFunc("POST /v1/auth/login", a.login)
	m.HandleFunc("POST /v1/auth/refresh", a.refresh)
	m.HandleFunc("GET /", a.loginPage)
	m.HandleFunc("POST /dashboard/login", a.dashboardLogin)
	m.HandleFunc("POST /v1/webhooks/sandbox", a.webhook)
	m.Handle("POST /v1/api-keys", a.require("keys:write", http.HandlerFunc(a.createKey)))
	m.Handle("POST /v1/usage", a.require("usage:write", http.HandlerFunc(a.usage)))
	m.Handle("GET /v1/subscription", a.require("subscription:read", http.HandlerFunc(a.currentSubscription)))
	m.Handle("POST /v1/subscription/change", a.require("subscription:write", http.HandlerFunc(a.changePlan)))
	m.Handle("GET /v1/invoices", a.require("billing:read", http.HandlerFunc(a.listInvoices)))
	m.Handle("POST /v1/invoices/generate", a.require("billing:write", http.HandlerFunc(a.generateInvoice)))
	m.Handle("POST /v1/invoices/{invoiceID}/pay", a.require("billing:write", http.HandlerFunc(a.createPayment)))
	m.Handle("GET /v1/admin/audit-logs", a.require("admin:audit", http.HandlerFunc(a.auditLogs)))
	m.Handle("GET /dashboard", a.require("subscription:read", http.HandlerFunc(a.dashboardPage)))
	return a.metrics.Wrap(a.requestLog(m))
}
func (a *App) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			rid = platform.ID()
		}
		w.Header().Set("X-Request-ID", rid)
		next.ServeHTTP(w, r)
		slog.Info("http_request", "method", r.Method, "path", r.URL.Path, "request_id", rid, "duration_ms", time.Since(start).Milliseconds())
	})
}
func (a *App) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.db.Ping(ctx); err != nil {
		problem(w, 503, "database unavailable")
		return
	}
	if err := a.redis.Ping(ctx).Err(); err != nil {
		problem(w, 503, "redis unavailable")
		return
	}
	jsonOut(w, 200, map[string]string{"status": "ready"})
}

func (a *App) require(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		credential, err := auth.Bearer(r.Header.Get("Authorization"))
		if err != nil {
			if c, e := r.Cookie("meterflow_access"); e == nil {
				credential = c.Value
				err = nil
			}
		}
		if err != nil {
			problem(w, 401, err.Error())
			return
		}
		var p tenant.Principal
		if strings.HasPrefix(credential, "mf_live_") {
			var scopes []string
			err = a.db.QueryRow(r.Context(), `SELECT id,tenant_id,scopes FROM api_keys WHERE secret_hash=$1 AND revoked_at IS NULL`, auth.Hash(credential)).Scan(&p.APIKeyID, &p.TenantID, &scopes)
			if err == nil {
				p.Scopes = map[string]bool{}
				for _, s := range scopes {
					p.Scopes[s] = true
				}
				_, _ = a.db.Exec(r.Context(), `UPDATE api_keys SET last_used_at=now() WHERE id=$1`, p.APIKeyID)
			}
		} else {
			var c auth.Claims
			c, err = auth.Verify(a.cfg.JWTSecret, credential, time.Now())
			if err == nil {
				p = tenant.Principal{UserID: c.Subject, TenantID: c.TenantID, Role: c.Role}
			}
		}
		if err != nil || p.TenantID == "" {
			problem(w, 401, "invalid credential")
			return
		}
		if !p.Can(scope) {
			problem(w, 403, "insufficient scope or role")
			return
		}
		next.ServeHTTP(w, r.WithContext(tenant.With(r.Context(), p)))
	})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password, Tenant string }
	if decode(r, &in) != nil {
		problem(w, 400, "invalid json")
		return
	}
	s, err := a.auth.Login(r.Context(), in.Email, in.Password, in.Tenant)
	if err != nil {
		problem(w, 401, "invalid credentials")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "meterflow_access", Value: s.AccessToken, Path: "/dashboard", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.cfg.Env == "production", MaxAge: int(a.cfg.AccessTTL.Seconds())})
	jsonOut(w, 200, s)
}
func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, loginHTML)
}
func (a *App) dashboardLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		problem(w, 400, "invalid form")
		return
	}
	s, err := a.auth.Login(r.Context(), r.FormValue("email"), r.FormValue("password"), r.FormValue("tenant"))
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "meterflow_access", Value: s.AccessToken, Path: "/dashboard", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.cfg.Env == "production", MaxAge: int(a.cfg.AccessTTL.Seconds())})
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
func (a *App) refresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refresh_token"`
		Tenant       string `json:"tenant"`
	}
	if decode(r, &in) != nil {
		problem(w, 400, "invalid json")
		return
	}
	s, err := a.auth.Refresh(r.Context(), in.RefreshToken, in.Tenant)
	if err != nil {
		problem(w, 401, "invalid refresh token")
		return
	}
	jsonOut(w, 200, s)
}
func (a *App) createKey(w http.ResponseWriter, r *http.Request) {
	p, _ := tenant.From(r.Context())
	var in struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if decode(r, &in) != nil || in.Name == "" {
		problem(w, 400, "name and scopes required")
		return
	}
	allowed := map[string]bool{"usage:write": true, "subscription:read": true, "subscription:write": true, "billing:read": true, "billing:write": true, "keys:write": true}
	for _, s := range in.Scopes {
		if !allowed[s] {
			problem(w, 400, "unknown scope: "+s)
			return
		}
	}
	plain, prefix, hash, err := auth.APIKey()
	if err != nil {
		serverError(w, err)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	keyID := platform.ID()
	_, err = tx.Exec(r.Context(), `INSERT INTO api_keys(id,tenant_id,name,key_prefix,secret_hash,scopes) VALUES($1,$2,$3,$4,$5,$6)`, keyID, p.TenantID, in.Name, prefix, hash, in.Scopes)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(id,tenant_id,actor_type,actor_id,action,resource_type,resource_id,data) VALUES($1,$2,'user',$3,'api_key.created','api_key',$4,$5)`, platform.ID(), p.TenantID, p.UserID, keyID, []byte(`{}`))
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, 201, map[string]any{"api_key": plain, "prefix": prefix, "warning": "shown once"})
}
func (a *App) usage(w http.ResponseWriter, r *http.Request) {
	p, _ := tenant.From(r.Context())
	ok, err := a.meter.Allow(r.Context(), p.TenantID)
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok {
		problem(w, 429, "rate limit exceeded")
		return
	}
	var in struct {
		IdempotencyKey string         `json:"idempotency_key"`
		Meter          string         `json:"meter"`
		Quantity       int64          `json:"quantity"`
		OccurredAt     time.Time      `json:"occurred_at"`
		Metadata       map[string]any `json:"metadata"`
	}
	if decode(r, &in) != nil {
		problem(w, 400, "invalid json")
		return
	}
	e, err := a.meter.Record(r.Context(), p.TenantID, in.IdempotencyKey, in.Meter, in.Quantity, in.OccurredAt, in.Metadata)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	code := 201
	if e.Duplicate {
		code = 200
	}
	jsonOut(w, code, e)
}
func (a *App) currentSubscription(w http.ResponseWriter, r *http.Request) {
	p, _ := tenant.From(r.Context())
	v, err := a.subs.Current(r.Context(), p.TenantID)
	if err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, 200, v)
}
func (a *App) changePlan(w http.ResponseWriter, r *http.Request) {
	p, _ := tenant.From(r.Context())
	var in struct {
		PlanCode string `json:"plan_code"`
	}
	if decode(r, &in) != nil {
		problem(w, 400, "invalid json")
		return
	}
	if err := a.subs.ChangePlan(r.Context(), p.TenantID, in.PlanCode, r.Header.Get("Idempotency-Key"), time.Now().UTC()); err != nil {
		problem(w, 409, err.Error())
		return
	}
	jsonOut(w, 200, map[string]string{"status": "scheduled_or_applied"})
}
func (a *App) listInvoices(w http.ResponseWriter, r *http.Request) {
	p, _ := tenant.From(r.Context())
	v, err := a.bills.List(r.Context(), p.TenantID)
	if err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, 200, map[string]any{"invoices": v})
}
func (a *App) generateInvoice(w http.ResponseWriter, r *http.Request) {
	p, _ := tenant.From(r.Context())
	var in struct {
		Start time.Time `json:"period_start"`
		End   time.Time `json:"period_end"`
	}
	if decode(r, &in) != nil {
		problem(w, 400, "invalid json")
		return
	}
	v, err := a.bills.Generate(r.Context(), p.TenantID, in.Start, in.End)
	if err != nil {
		problem(w, 409, err.Error())
		return
	}
	jsonOut(w, 201, v)
}
func (a *App) createPayment(w http.ResponseWriter, r *http.Request) {
	p, _ := tenant.From(r.Context())
	v, err := a.pay.CreateSandbox(r.Context(), p.TenantID, r.PathValue("invoiceID"), r.Header.Get("Idempotency-Key"))
	if err != nil {
		problem(w, 409, err.Error())
		return
	}
	jsonOut(w, 201, v)
}
func (a *App) auditLogs(w http.ResponseWriter, r *http.Request) {
	p, _ := tenant.From(r.Context())
	rows, err := a.db.Query(r.Context(), `SELECT id,actor_type,COALESCE(actor_id,''),action,resource_type,COALESCE(resource_id,''),data,created_at FROM audit_logs WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 100`, p.TenantID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, actorType, actorID, action, resourceType, resourceID string
		var data json.RawMessage
		var created time.Time
		if err = rows.Scan(&id, &actorType, &actorID, &action, &resourceType, &resourceID, &data, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "actor_type": actorType, "actor_id": actorID, "action": action, "resource_type": resourceType, "resource_id": resourceID, "data": data, "created_at": created})
	}
	jsonOut(w, 200, map[string]any{"audit_logs": items})
}
func (a *App) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		problem(w, 400, "invalid body")
		return
	}
	if err = a.pay.Verify(r.Header.Get("X-Sandbox-Timestamp"), r.Header.Get("X-Sandbox-Signature"), body, time.Now()); err != nil {
		problem(w, 401, err.Error())
		return
	}
	duplicate, err := a.pay.Handle(r.Context(), body)
	if err != nil {
		problem(w, 422, err.Error())
		return
	}
	jsonOut(w, 200, map[string]bool{"duplicate": duplicate})
}
func (a *App) dashboardPage(w http.ResponseWriter, r *http.Request) {
	p, _ := tenant.From(r.Context())
	sub, err := a.subs.Current(r.Context(), p.TenantID)
	if err != nil {
		serverError(w, err)
		return
	}
	used, _ := a.meter.Summary(r.Context(), p.TenantID, sub.PeriodStart, sub.PeriodEnd)
	invoices, _ := a.bills.List(r.Context(), p.TenantID)
	admin := p.Role == "owner" || p.Role == "admin"
	stats := map[string]int64{}
	if admin {
		var keys, dlq, outbox int64
		_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM api_keys WHERE tenant_id=$1 AND revoked_at IS NULL`, p.TenantID).Scan(&keys)
		_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM dead_letters d JOIN outbox_events o ON o.id=d.original_event_id WHERE o.tenant_id=$1`, p.TenantID).Scan(&dlq)
		_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM outbox_events WHERE tenant_id=$1 AND processed_at IS NULL`, p.TenantID).Scan(&outbox)
		stats = map[string]int64{"Keys": keys, "DLQ": dlq, "Outbox": outbox}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.dashboard.Execute(w, map[string]any{"Sub": sub, "Used": used, "Invoices": invoices, "Admin": admin, "Stats": stats})
}

func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, detail string) {
	jsonOut(w, status, map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "detail": detail})
}
func serverError(w http.ResponseWriter, err error) {
	slog.Error("request failed", "error", err)
	problem(w, 500, "internal server error")
}

const dashboardHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>MeterFlow</title><style>
:root{font-family:Inter,system-ui;color:#15202b;background:#f5f7fa}body{margin:0}.wrap{max-width:1050px;margin:auto;padding:32px}header{display:flex;justify-content:space-between;align-items:center}.badge{background:#dff7ea;color:#087443;padding:6px 12px;border-radius:20px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:18px}.card{background:white;border:1px solid #e4e9ef;border-radius:14px;padding:22px;box-shadow:0 5px 18px #23334d0b}.big{font-size:30px;font-weight:750;margin:8px 0}.muted{color:#64748b}progress{width:100%;height:12px}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:12px;border-bottom:1px solid #edf0f3}.admin{border-left:4px solid #5b5bd6}</style></head><body><div class="wrap"><header><div><h1>MeterFlow</h1><p class="muted">Customer billing dashboard</p></div><span class="badge">{{.Sub.Status}}</span></header><div class="grid"><section class="card"><span class="muted">Current plan</span><div class="big">{{.Sub.PlanName}}</div><div>{{.Sub.Currency}} {{.Sub.MonthlyPrice}} / month</div></section><section class="card"><span class="muted">Usage</span><div class="big">{{.Used}} / {{.Sub.IncludedUnits}}</div><progress value="{{.Used}}" max="{{.Sub.IncludedUnits}}"></progress></section><section class="card"><span class="muted">Period</span><div class="big">Active</div><div>{{.Sub.PeriodStart.Format "02 Jan"}} – {{.Sub.PeriodEnd.Format "02 Jan 2006"}}</div></section>{{if .Admin}}<section class="card admin"><span class="muted">Admin operations</span><div class="big">{{index .Stats "Outbox"}} queued</div><div>{{index .Stats "Keys"}} active API keys · {{index .Stats "DLQ"}} dead letters</div></section>{{end}}</div><section class="card" style="margin-top:18px"><h2>Invoices</h2><table><thead><tr><th>ID</th><th>Period</th><th>Total</th><th>Status</th></tr></thead><tbody>{{range .Invoices}}<tr><td>{{.ID}}</td><td>{{.PeriodStart.Format "02 Jan"}} – {{.PeriodEnd.Format "02 Jan 2006"}}</td><td>{{.Currency}} {{.TotalMinor}}</td><td>{{.Status}}</td></tr>{{else}}<tr><td colspan="4">No invoices yet.</td></tr>{{end}}</tbody></table></section></div></body></html>`

const loginHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>MeterFlow login</title><style>:root{font-family:Inter,system-ui;background:#f3f6fa;color:#15202b}main{max-width:380px;margin:10vh auto;background:#fff;padding:32px;border-radius:16px;box-shadow:0 15px 45px #23334d18}label{display:block;margin:16px 0 5px}input{box-sizing:border-box;width:100%;padding:12px;border:1px solid #ccd5df;border-radius:8px}button{width:100%;margin-top:22px;padding:12px;border:0;border-radius:8px;background:#4f46e5;color:#fff;font-weight:700}.muted{color:#64748b}</style></head><body><main><h1>MeterFlow</h1><p class="muted">Sign in to your billing workspace.</p><form method="post" action="/dashboard/login"><label>Email</label><input name="email" type="email" value="owner@acme.test" required><label>Password</label><input name="password" type="password" value="demo-password" required><label>Tenant</label><input name="tenant" value="acme" required><button>Open dashboard</button></form></main></body></html>`
