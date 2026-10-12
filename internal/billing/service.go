package billing

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/example/meterflow/internal/money"
	"github.com/example/meterflow/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct{ DB *pgxpool.Pool }
type Invoice struct {
	ID            string    `json:"id"`
	Currency      string    `json:"currency"`
	Status        string    `json:"status"`
	PeriodStart   time.Time `json:"period_start"`
	PeriodEnd     time.Time `json:"period_end"`
	SubtotalMinor int64     `json:"subtotal_minor"`
	TotalMinor    int64     `json:"total_minor"`
}

func (s Service) Generate(ctx context.Context, tenant string, start, end time.Time) (Invoice, error) {
	if !end.After(start) {
		return Invoice{}, errors.New("invalid period")
	}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Invoice{}, err
	}
	defer tx.Rollback(ctx)
	var sid, currency string
	var base, included, overage, used int64
	err = tx.QueryRow(ctx, `SELECT s.id,p.currency,p.monthly_price_minor,p.included_units,p.overage_price_minor FROM subscriptions s JOIN plans p ON p.id=s.plan_id WHERE s.tenant_id=$1 AND s.status IN ('active','past_due') FOR UPDATE`, tenant).Scan(&sid, &currency, &base, &included, &overage)
	if err != nil {
		return Invoice{}, err
	}
	var existing Invoice
	err = tx.QueryRow(ctx, `SELECT id,currency,status,period_start,period_end,subtotal_minor,total_minor FROM invoices WHERE subscription_id=$1 AND period_start=$2 AND period_end=$3`, sid, start, end).Scan(&existing.ID, &existing.Currency, &existing.Status, &existing.PeriodStart, &existing.PeriodEnd, &existing.SubtotalMinor, &existing.TotalMinor)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Invoice{}, err
	}
	err = tx.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0) FROM usage_events WHERE tenant_id=$1 AND occurred_at >= $2 AND occurred_at < $3`, tenant, start, end).Scan(&used)
	if err != nil {
		return Invoice{}, err
	}
	overageUnits := used - included
	if overageUnits < 0 {
		overageUnits = 0
	}
	overageAmount, err := money.Mul(overageUnits, overage)
	if err != nil {
		return Invoice{}, err
	}
	var credit, charge int64
	_ = tx.QueryRow(ctx, `SELECT COALESCE(sum(proration_credit_minor),0),COALESCE(sum(proration_charge_minor),0) FROM subscription_changes WHERE tenant_id=$1 AND effective_at >= $2 AND effective_at < $3`, tenant, start, end).Scan(&credit, &charge)
	total, err := money.Sum(base, overageAmount, -credit, charge)
	if err != nil {
		return Invoice{}, err
	}
	if total < 0 {
		total = 0
	}
	id := platform.ID()
	_, err = tx.Exec(ctx, `INSERT INTO invoices(id,tenant_id,subscription_id,period_start,period_end,currency,subtotal_minor,total_minor,status,due_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'open',$9)`, id, tenant, sid, start, end, currency, total, total, time.Now().UTC().Add(7*24*time.Hour))
	if err != nil {
		return Invoice{}, err
	}
	type line struct {
		kind, desc      string
		q, unit, amount int64
	}
	lines := []line{{"subscription", "Monthly subscription", 1, base, base}, {"overage", "Usage overage", overageUnits, overage, overageAmount}}
	if credit > 0 {
		lines = append(lines, line{"proration_credit", "Unused previous plan credit", 1, -credit, -credit})
	}
	if charge > 0 {
		lines = append(lines, line{"proration_charge", "New plan prorated charge", 1, charge, charge})
	}
	for _, l := range lines {
		if l.amount == 0 && l.kind != "subscription" {
			continue
		}
		_, err = tx.Exec(ctx, `INSERT INTO invoice_lines(id,tenant_id,invoice_id,kind,description,quantity,unit_amount_minor,amount_minor) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, platform.ID(), tenant, id, l.kind, l.desc, l.q, l.unit, l.amount)
		if err != nil {
			return Invoice{}, err
		}
	}
	payload, _ := json.Marshal(map[string]any{"invoice_id": id, "tenant_id": tenant, "total_minor": total, "currency": currency})
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,tenant_id,topic,aggregate_id,payload) VALUES($1,$2,'invoice.created',$3,$4)`, platform.ID(), tenant, id, payload)
	if err != nil {
		return Invoice{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,actor_type,action,resource_type,resource_id,data) VALUES($1,$2,'system','invoice.created','invoice',$3,$4)`, platform.ID(), tenant, id, payload)
	if err != nil {
		return Invoice{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Invoice{}, err
	}
	return Invoice{id, currency, "open", start, end, total, total}, nil
}
func (s Service) List(ctx context.Context, tenant string) ([]Invoice, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,currency,status,period_start,period_end,subtotal_minor,total_minor FROM invoices WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 100`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invoice
	for rows.Next() {
		var x Invoice
		if err = rows.Scan(&x.ID, &x.Currency, &x.Status, &x.PeriodStart, &x.PeriodEnd, &x.SubtotalMinor, &x.TotalMinor); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
