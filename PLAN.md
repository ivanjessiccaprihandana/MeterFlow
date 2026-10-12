# MeterFlow implementation plan

1. **Foundation** — Go module, configuration, PostgreSQL/Redis connections, migrations and demo seed, auth/refresh-token rotation, tenant middleware, RBAC, scoped API keys, logging, metrics, health checks, Docker Compose, and CI.
2. **Subscriptions and metering** — plans/prices/quotas, subscription lifecycle, idempotent usage ingestion, Redis-backed tenant rate limits, and transactional usage aggregation.
3. **Billing and payments** — deterministic integer-money invoicing, documented proration, overage charges, sandbox payments, signed webhooks, transactional outbox, retry/backoff, DLQ, and duplicate/out-of-order handling.
4. **Dashboard** — lightweight server-rendered customer/admin views over the same tenant-safe services.
5. **Verification** — unit and integration tests, OpenAPI contract, migration smoke checks, k6 load scenario, CI automation, and honest measurement notes.

Each stage is committed as runnable code before the next stage is layered on. Verification state and limitations are recorded in `PROGRESS.md`.
