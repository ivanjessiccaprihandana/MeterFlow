package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env, HTTPAddr, DatabaseURL, RedisAddr, RedisPassword, JWTSecret, WebhookSecret string
	AccessTTL, RefreshTTL, WorkerPoll                                              time.Duration
	WorkerMaxAttempts, RateLimitPerMinute                                          int
}

func Load() (Config, error) {
	c := Config{
		Env: get("APP_ENV", "development"), HTTPAddr: get("HTTP_ADDR", ":8080"),
		DatabaseURL: get("DATABASE_URL", "postgres://meterflow:meterflow@localhost:5432/meterflow?sslmode=disable"),
		RedisAddr:   get("REDIS_ADDR", "localhost:6379"), RedisPassword: os.Getenv("REDIS_PASSWORD"),
		JWTSecret: os.Getenv("JWT_SECRET"), WebhookSecret: get("WEBHOOK_SECRET", "whsec_demo_only"),
	}
	if len(c.JWTSecret) < 32 {
		return c, fmt.Errorf("JWT_SECRET must contain at least 32 bytes")
	}
	var err error
	if c.AccessTTL, err = duration("ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		return c, err
	}
	if c.RefreshTTL, err = duration("REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		return c, err
	}
	if c.WorkerPoll, err = duration("WORKER_POLL_INTERVAL", time.Second); err != nil {
		return c, err
	}
	if c.WorkerMaxAttempts, err = integer("WORKER_MAX_ATTEMPTS", 8); err != nil {
		return c, err
	}
	if c.RateLimitPerMinute, err = integer("RATE_LIMIT_PER_MINUTE", 6000); err != nil {
		return c, err
	}
	return c, nil
}
func get(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func duration(k string, d time.Duration) (time.Duration, error) {
	v := get(k, d.String())
	x, e := time.ParseDuration(v)
	if e != nil {
		return 0, fmt.Errorf("%s: %w", k, e)
	}
	return x, nil
}
func integer(k string, d int) (int, error) {
	v := get(k, strconv.Itoa(d))
	x, e := strconv.Atoi(v)
	if e != nil {
		return 0, fmt.Errorf("%s: %w", k, e)
	}
	return x, nil
}
