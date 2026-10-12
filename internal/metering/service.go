package metering

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/example/meterflow/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	DB    *pgxpool.Pool
	Redis *redis.Client
	Limit int
}
type Event struct {
	ID         string    `json:"id"`
	Meter      string    `json:"meter"`
	Quantity   int64     `json:"quantity"`
	OccurredAt time.Time `json:"occurred_at"`
	Duplicate  bool      `json:"duplicate"`
}

func (s Service) Allow(ctx context.Context, tenant string) (bool, error) {
	minute := time.Now().UTC().Format("200601021504")
	key := "rate:" + tenant + ":" + minute
	const script = `local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]) end; return n`
	n, err := s.Redis.Eval(ctx, script, []string{key}, int64((2*time.Minute)/time.Millisecond)).Int64()
	if err != nil {
		return false, err
	}
	return n <= int64(s.Limit), nil
}
func (s Service) Record(ctx context.Context, tenant, idem, meter string, quantity int64, at time.Time, metadata map[string]any) (Event, error) {
	if idem == "" || len(idem) > 200 || meter == "" || len(meter) > 100 || quantity <= 0 {
		return Event{}, errors.New("idempotency_key, meter, and positive quantity are required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if at.After(time.Now().Add(5 * time.Minute)) {
		return Event{}, errors.New("occurred_at is too far in the future")
	}
	b, _ := json.Marshal(metadata)
	id := platform.ID()
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO usage_events(id,tenant_id,idempotency_key,meter,quantity,occurred_at,metadata) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,idempotency_key) DO NOTHING`, id, tenant, idem, meter, quantity, at, b)
	if err != nil {
		return Event{}, err
	}
	if tag.RowsAffected() == 0 {
		var e Event
		err = tx.QueryRow(ctx, `SELECT id,meter,quantity,occurred_at FROM usage_events WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, idem).Scan(&e.ID, &e.Meter, &e.Quantity, &e.OccurredAt)
		e.Duplicate = true
		return e, err
	}
	payload, _ := json.Marshal(map[string]any{"usage_event_id": id, "meter": meter, "quantity": quantity})
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,tenant_id,topic,aggregate_id,payload) VALUES($1,$2,'usage.recorded',$3,$4)`, platform.ID(), tenant, id, payload); err != nil {
		return Event{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,actor_type,action,resource_type,resource_id,data) VALUES($1,$2,'api_key','usage.recorded','usage_event',$3,$4)`, platform.ID(), tenant, id, b); err != nil {
		return Event{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Event{}, err
	}
	return Event{ID: id, Meter: meter, Quantity: quantity, OccurredAt: at}, nil
}
func (s Service) Summary(ctx context.Context, tenant string, start, end time.Time) (used int64, err error) {
	if !end.After(start) {
		return 0, fmt.Errorf("end must be after start")
	}
	err = s.DB.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0) FROM usage_events WHERE tenant_id=$1 AND occurred_at >= $2 AND occurred_at < $3`, tenant, start, end).Scan(&used)
	return
}
