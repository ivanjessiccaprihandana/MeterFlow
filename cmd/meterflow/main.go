package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/meterflow/internal/config"
	"github.com/example/meterflow/internal/httpapi"
	"github.com/example/meterflow/internal/platform"
	"github.com/example/meterflow/internal/subscription"
	"github.com/example/meterflow/internal/worker"
	"github.com/redis/go-redis/v9"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := platform.OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "migrate":
		err = platform.Migrate(ctx, db)
	case "seed":
		err = platform.Seed(ctx, db)
	case "worker":
		subscriptions := subscription.Service{DB: db}
		w := worker.Worker{DB: db, Poll: cfg.WorkerPoll, MaxAttempts: cfg.WorkerMaxAttempts, Handlers: map[string]func(context.Context, []byte) error{
			"usage.recorded":       func(context.Context, []byte) error { return nil },
			"subscription.changed": subscriptions.ApplyChange,
			"invoice.created": func(_ context.Context, p []byte) error {
				slog.Info("sandbox invoice notification", "payload", string(p))
				return nil
			},
			"payment.created": func(_ context.Context, p []byte) error {
				slog.Info("sandbox payment created", "payload", string(p))
				return nil
			},
		}}
		err = w.Run(ctx)
	case "serve":
		rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
		defer rdb.Close()
		app := httpapi.New(cfg, db, rdb)
		srv := &http.Server{Addr: cfg.HTTPAddr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}()
		slog.Info("server listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		err = srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	default:
		slog.Error("unknown command", "command", cmd)
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("command failed", "command", cmd, "error", err)
		os.Exit(1)
	}
}
