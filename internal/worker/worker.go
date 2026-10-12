package worker

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/example/meterflow/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	DB          *pgxpool.Pool
	Poll        time.Duration
	MaxAttempts int
	Handlers    map[string]func(context.Context, []byte) error
}
type job struct {
	id, topic string
	payload   []byte
	attempts  int
}

func (w Worker) Run(ctx context.Context) error {
	t := time.NewTicker(w.Poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := w.once(ctx); err != nil {
				slog.Error("worker poll failed", "error", err)
			}
		}
	}
}
func (w Worker) once(ctx context.Context) error {
	tx, err := w.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var j job
	err = tx.QueryRow(ctx, `SELECT id,topic,payload,attempts FROM outbox_events WHERE processed_at IS NULL AND available_at<=now() AND (locked_at IS NULL OR locked_at<now()-interval '5 minutes') ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&j.id, &j.topic, &j.payload, &j.attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE outbox_events SET locked_at=now() WHERE id=$1`, j.id); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	h := w.Handlers[j.topic]
	if h == nil {
		h = func(context.Context, []byte) error { return nil }
	}
	err = h(ctx, j.payload)
	if err == nil {
		_, e := w.DB.Exec(ctx, `UPDATE outbox_events SET processed_at=now(),locked_at=NULL WHERE id=$1 AND processed_at IS NULL`, j.id)
		return e
	}
	j.attempts++
	if j.attempts >= w.MaxAttempts {
		tx, e := w.DB.Begin(ctx)
		if e != nil {
			return e
		}
		defer tx.Rollback(ctx)
		_, e = tx.Exec(ctx, `INSERT INTO dead_letters(id,original_event_id,topic,payload,attempts,last_error) SELECT $1,id,topic,payload,$2,$3 FROM outbox_events WHERE id=$4`, platform.ID(), j.attempts, err.Error(), j.id)
		if e == nil {
			_, e = tx.Exec(ctx, `UPDATE outbox_events SET processed_at=now(),last_error=$2,attempts=$3,locked_at=NULL WHERE id=$1`, j.id, err.Error(), j.attempts)
		}
		if e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	delay := time.Duration(math.Min(math.Pow(2, float64(j.attempts)), 300)) * time.Second
	_, e := w.DB.Exec(ctx, `UPDATE outbox_events SET attempts=$2,last_error=$3,available_at=now()+$4::interval,locked_at=NULL WHERE id=$1`, j.id, j.attempts, err.Error(), delay.String())
	return e
}
