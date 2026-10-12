package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/example/meterflow/internal/money"
	"github.com/example/meterflow/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct{ DB *pgxpool.Pool }
type View struct {
	ID                string    `json:"id"`
	PlanCode          string    `json:"plan_code"`
	PlanName          string    `json:"plan_name"`
	Currency          string    `json:"currency"`
	Status            string    `json:"status"`
	MonthlyPrice      int64     `json:"monthly_price_minor"`
	IncludedUnits     int64     `json:"included_units"`
	OveragePrice      int64     `json:"overage_price_minor"`
	PeriodStart       time.Time `json:"current_period_start"`
	PeriodEnd         time.Time `json:"current_period_end"`
	CancelAtPeriodEnd bool      `json:"cancel_at_period_end"`
}
type changePayload struct {
	ChangeID       string    `json:"change_id"`
	SubscriptionID string    `json:"subscription_id"`
	ToPlanID       string    `json:"to_plan_id"`
	EffectiveAt    time.Time `json:"effective_at"`
}

func (s Service) Current(ctx context.Context, tenant string) (View, error) {
	var v View
	err := s.DB.QueryRow(ctx, `SELECT s.id,p.code,p.name,p.currency,s.status,p.monthly_price_minor,p.included_units,p.overage_price_minor,s.current_period_start,s.current_period_end,s.cancel_at_period_end FROM subscriptions s JOIN plans p ON p.id=s.plan_id WHERE s.tenant_id=$1 AND s.status IN ('active','past_due')`, tenant).Scan(&v.ID, &v.PlanCode, &v.PlanName, &v.Currency, &v.Status, &v.MonthlyPrice, &v.IncludedUnits, &v.OveragePrice, &v.PeriodStart, &v.PeriodEnd, &v.CancelAtPeriodEnd)
	return v, err
}

func (s Service) ChangePlan(ctx context.Context, tenant, code, idem string, now time.Time) error {
	if code == "" || idem == "" || len(idem) > 200 {
		return errors.New("plan_code and Idempotency-Key required")
	}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	hash := sha256.Sum256([]byte(code))
	tag, err := tx.Exec(ctx, `INSERT INTO idempotency_keys(tenant_id,key,operation,request_hash,status_code,expires_at) VALUES($1,$2,'subscription.change',$3,200,now()+interval '24 hours') ON CONFLICT DO NOTHING`, tenant, idem, hash[:])
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var saved []byte
		if err = tx.QueryRow(ctx, `SELECT request_hash FROM idempotency_keys WHERE tenant_id=$1 AND key=$2 AND operation='subscription.change'`, tenant, idem).Scan(&saved); err != nil {
			return err
		}
		if string(saved) != string(hash[:]) {
			return errors.New("idempotency key reused with different request")
		}
		return nil
	}
	var sid, fromID, toID string
	var fromPrice, toPrice int64
	var start, end time.Time
	err = tx.QueryRow(ctx, `SELECT s.id,s.plan_id,p.monthly_price_minor,s.current_period_start,s.current_period_end FROM subscriptions s JOIN plans p ON p.id=s.plan_id WHERE s.tenant_id=$1 AND s.status='active' FOR UPDATE`, tenant).Scan(&sid, &fromID, &fromPrice, &start, &end)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT id,monthly_price_minor FROM plans WHERE code=$1 AND active`, code).Scan(&toID, &toPrice)
	if err != nil {
		return err
	}
	if fromID == toID {
		return tx.Commit(ctx)
	}
	effective := now
	credit, charge := int64(0), int64(0)
	if toPrice < fromPrice {
		effective = end
	} else {
		total := end.Sub(start).Seconds()
		remaining := end.Sub(now).Seconds()
		if remaining < 0 {
			remaining = 0
		}
		credit, err = money.Prorate(fromPrice, int64(remaining), int64(total))
		if err != nil {
			return err
		}
		charge, err = money.Prorate(toPrice, int64(remaining), int64(total))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE subscriptions SET plan_id=$1,version=version+1,updated_at=now() WHERE id=$2`, toID, sid)
		if err != nil {
			return err
		}
	}
	change := platform.ID()
	_, err = tx.Exec(ctx, `INSERT INTO subscription_changes(id,tenant_id,subscription_id,from_plan_id,to_plan_id,effective_at,proration_credit_minor,proration_charge_minor) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, change, tenant, sid, fromID, toID, effective, credit, charge)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(changePayload{change, sid, toID, effective})
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,tenant_id,topic,aggregate_id,payload,available_at) VALUES($1,$2,'subscription.changed',$3,$4,$5)`, platform.ID(), tenant, sid, payload, effective)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,actor_type,action,resource_type,resource_id,data) VALUES($1,$2,'user','subscription.plan_changed','subscription',$3,$4)`, platform.ID(), tenant, sid, payload)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ApplyChange is replay-safe: only a still-different plan is changed.
func (s Service) ApplyChange(ctx context.Context, payload []byte) error {
	var p changePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	if time.Now().UTC().Before(p.EffectiveAt) {
		return errors.New("change is not due")
	}
	_, err := s.DB.Exec(ctx, `UPDATE subscriptions SET plan_id=$1,version=version+1,updated_at=now() WHERE id=$2 AND plan_id<>$1`, p.ToPlanID, p.SubscriptionID)
	return err
}
