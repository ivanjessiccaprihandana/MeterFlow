# MeterFlow

MeterFlow is a portfolio-grade SaaS billing backend demonstrating multi-tenant
subscriptions, usage metering, deterministic integer-money billing, sandbox payments,
and resilient asynchronous processing. It is a modular monolith written in Go with
PostgreSQL and Redis.

This repository is **not claimed production-ready**. In particular, operational
hardening, external payment-provider certification, security review, disaster-recovery
drills, and sustained benchmark evidence are still required.

## Quick start

```sh
cp .env.example .env
docker compose up -d --build postgres redis
docker compose run --rm api migrate
docker compose run --rm api seed
docker compose up -d api worker
```

Open `http://localhost:8080/health/ready`. The demo identity is
`owner@acme.test` / `demo-password`, tenant `acme`.

```sh
curl -s http://localhost:8080/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"owner@acme.test","password":"demo-password","tenant":"acme"}'
```

Use the returned access token as `Authorization: Bearer <token>`. For the browser
dashboard, open `/`, sign in with the demo identity, and the server sets a short-lived
HttpOnly dashboard cookie.

## Modules

- `internal/auth`: access tokens, refresh rotation/reuse detection, bcrypt passwords,
  and hashed API keys.
- `internal/tenant`: tenant principal and authorization context.
- `internal/subscription`: active plans, immediate upgrade proration, scheduled downgrade.
- `internal/metering`: immutable/idempotent usage events and Redis rate limiting.
- `internal/billing`: transactionally generated subscription, overage, and proration lines.
- `internal/payment`: signed sandbox webhooks with duplicate and out-of-order handling.
- `internal/worker`: transactional outbox polling, capped exponential retry, and DLQ.
- `internal/httpapi`: API, health/metrics endpoints, and customer/admin dashboard.

See [billing rules](docs/BILLING.md), [architecture](docs/ARCHITECTURE.md), the
[OpenAPI contract](openapi.yaml), and [load-test protocol](docs/LOAD_TEST.md).

## Verification

```sh
go test ./...
go vet ./...
docker compose --profile test run --rm integration
docker compose --profile load run --rm k6 run /scripts/usage.js
```

CI runs formatting, vet, race-enabled unit tests, migrations, integration tests, and
an image build. Actual results for this workspace are tracked in `PROGRESS.md`.

## Security and correctness notes

- Every tenant-owned query includes a tenant identifier. The full external migration
  also enables PostgreSQL RLS as defense in depth.
- API-key secrets and refresh tokens are stored as SHA-256 digests; user passwords use
  bcrypt. Access tokens are short-lived HMAC JWTs.
- Usage idempotency, invoice period uniqueness, provider event uniqueness, transactions,
  and row locks guard the principal money-moving paths.
- Redis outages fail rate-limited usage ingestion closed. PostgreSQL remains the source
  of truth; no billing data is held only in Redis.
- Webhook signatures cover the exact body and timestamp; deliveries beyond five minutes
  are rejected.
