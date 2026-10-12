package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/example/meterflow/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB     *pgxpool.Pool
	Secret string
}
type Payment struct {
	ID                string `json:"id"`
	InvoiceID         string `json:"invoice_id"`
	ProviderPaymentID string `json:"provider_payment_id"`
	AmountMinor       int64  `json:"amount_minor"`
	Currency          string `json:"currency"`
	Status            string `json:"status"`
}
type Webhook struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
	Data       struct {
		PaymentID   string `json:"payment_id"`
		InvoiceID   string `json:"invoice_id"`
		Status      string `json:"status"`
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
	} `json:"data"`
}

func (s Service) Verify(timestamp, signature string, body []byte, now time.Time) error {
	sec, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("invalid timestamp")
	}
	at := time.Unix(sec, 0)
	if now.Sub(at) > 5*time.Minute || at.Sub(now) > time.Minute {
		return errors.New("stale timestamp")
	}
	mac := hmac.New(sha256.New, []byte(s.Secret))
	_, _ = mac.Write([]byte(timestamp + "." + string(body)))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(signature)), []byte(expected)) {
		return errors.New("invalid signature")
	}
	return nil
}
func (s Service) Handle(ctx context.Context, body []byte) (bool, error) {
	var e Webhook
	if err := json.Unmarshal(body, &e); err != nil {
		return false, err
	}
	if e.ID == "" || e.Data.PaymentID == "" {
		return false, errors.New("missing event fields")
	}
	if e.OccurredAt.IsZero() {
		return false, errors.New("occurred_at is required")
	}
	switch e.Data.Status {
	case "pending", "succeeded", "failed":
	default:
		return false, fmt.Errorf("unsupported payment status")
	}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO webhook_events(id,provider,provider_event_id,event_type,occurred_at,payload) VALUES($1,'sandbox',$2,$3,$4,$5) ON CONFLICT(provider,provider_event_id) DO NOTHING`, platform.ID(), e.ID, e.Type, e.OccurredAt, body)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return true, nil
	}
	var tenant, invoiceCurrency string
	var invoiceTotal int64
	err = tx.QueryRow(ctx, `SELECT tenant_id,total_minor,currency FROM invoices WHERE id=$1`, e.Data.InvoiceID).Scan(&tenant, &invoiceTotal, &invoiceCurrency)
	if err != nil {
		return false, err
	}
	if e.Data.AmountMinor != invoiceTotal || e.Data.Currency != invoiceCurrency {
		return false, errors.New("payment amount or currency does not match invoice")
	}
	tag, err = tx.Exec(ctx, `INSERT INTO payments(id,tenant_id,invoice_id,provider,provider_payment_id,amount_minor,currency,status,provider_event_at) VALUES($1,$2,$3,'sandbox',$4,$5,$6,$7,$8) ON CONFLICT(provider_payment_id) DO UPDATE SET status=excluded.status,provider_event_at=excluded.provider_event_at,updated_at=now() WHERE payments.provider_event_at IS NULL OR excluded.provider_event_at > payments.provider_event_at`, platform.ID(), tenant, e.Data.InvoiceID, e.Data.PaymentID, e.Data.AmountMinor, e.Data.Currency, e.Data.Status, e.OccurredAt)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 && e.Data.Status == "succeeded" {
		_, err = tx.Exec(ctx, `UPDATE invoices SET status='paid',paid_at=$2 WHERE id=$1 AND status='open'`, e.Data.InvoiceID, e.OccurredAt)
		if err != nil {
			return false, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE webhook_events SET status='processed',processed_at=now() WHERE provider='sandbox' AND provider_event_id=$1`, e.ID)
	if err != nil {
		return false, err
	}
	audit, _ := json.Marshal(map[string]any{"provider_event_id": e.ID, "status": e.Data.Status})
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,actor_type,actor_id,action,resource_type,resource_id,data) VALUES($1,$2,'webhook',$3,'payment.status_changed','payment',$4,$5)`, platform.ID(), tenant, e.ID, e.Data.PaymentID, audit)
	if err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func (s Service) CreateSandbox(ctx context.Context, tenant, invoiceID, idem string) (Payment, error) {
	if idem == "" || len(idem) > 200 {
		return Payment{}, errors.New("Idempotency-Key required")
	}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Payment{}, err
	}
	defer tx.Rollback(ctx)
	var p Payment
	providerHash := sha256.Sum256([]byte(invoiceID + ":" + idem))
	providerID := "pay_" + hex.EncodeToString(providerHash[:16])
	err = tx.QueryRow(ctx, `SELECT id,invoice_id,provider_payment_id,amount_minor,currency,status FROM payments WHERE tenant_id=$1 AND provider='sandbox' AND invoice_id=$2 AND provider_payment_id=$3`, tenant, invoiceID, providerID).Scan(&p.ID, &p.InvoiceID, &p.ProviderPaymentID, &p.AmountMinor, &p.Currency, &p.Status)
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, err
	}
	err = tx.QueryRow(ctx, `SELECT total_minor,currency FROM invoices WHERE id=$1 AND tenant_id=$2 AND status='open' FOR UPDATE`, invoiceID, tenant).Scan(&p.AmountMinor, &p.Currency)
	if err != nil {
		return Payment{}, err
	}
	p.ID, p.InvoiceID, p.ProviderPaymentID, p.Status = platform.ID(), invoiceID, providerID, "pending"
	_, err = tx.Exec(ctx, `INSERT INTO payments(id,tenant_id,invoice_id,provider,provider_payment_id,amount_minor,currency,status) VALUES($1,$2,$3,'sandbox',$4,$5,$6,'pending')`, p.ID, tenant, invoiceID, providerID, p.AmountMinor, p.Currency)
	if err != nil {
		return Payment{}, err
	}
	payload, _ := json.Marshal(map[string]any{"payment_id": providerID, "invoice_id": invoiceID})
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,tenant_id,topic,aggregate_id,payload) VALUES($1,$2,'payment.created',$3,$4)`, platform.ID(), tenant, p.ID, payload)
	if err != nil {
		return Payment{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,actor_type,action,resource_type,resource_id,data) VALUES($1,$2,'user','payment.created','payment',$3,$4)`, platform.ID(), tenant, p.ID, payload)
	if err != nil {
		return Payment{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Payment{}, err
	}
	return p, nil
}

func Sign(secret string, at time.Time, body []byte) (string, string) {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts + "." + string(body)))
	return ts, hex.EncodeToString(mac.Sum(nil))
}
