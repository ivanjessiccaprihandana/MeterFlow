# Architecture

MeterFlow is a modular monolith: one deployable serves HTTP, and the same binary runs
database migration, demo seeding, or the background worker. Module boundaries are
represented by focused packages under `internal/`; PostgreSQL is the source of truth,
and Redis is disposable acceleration for rate limiting.

The request path authenticates a short-lived access token or hashed scoped API key,
resolves an organization, authorizes a role/scope, and opens tenant-bound database
work. PostgreSQL row-level security is defense in depth, not a replacement for explicit
`tenant_id` predicates.

Domain transactions append outbox records. Workers claim jobs with `FOR UPDATE SKIP
LOCKED`, retry with capped exponential backoff, and move exhausted jobs to a dead-letter
table. Consumers must still be idempotent.
