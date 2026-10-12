# Billing rules

MeterFlow stores money as signed 64-bit integer minor units (`IDR` has no fractional
minor unit; USD cents would use the same representation). Floating point is never
used for money.

## Period boundaries

All instants are UTC. Billing periods are half-open `[start, end)`: an event exactly
at `period_end` belongs to the next period. A monthly subscription advances by one
calendar month while retaining the day when possible; anchors on the 29th–31st clamp
to the last day of shorter months.

## Usage and overage

Accepted usage is immutable. The invoice sums positive quantities by tenant and
period. Overage is `max(0, usage - included_units) * overage_price_minor`. The unique
`(tenant_id, idempotency_key)` constraint makes ingestion replay-safe.

## Plan changes and proration

- Downgrades take effect at the next period boundary; no current-period credit.
- Upgrades take effect immediately.
- Immediate upgrade proration uses seconds remaining divided by total seconds in
  the period, calculated independently for the unused old price credit and new price
  charge.
- Division rounds half-up: `(amount * remaining_seconds + period_seconds/2) /
  period_seconds`. Each result is stored as a separate immutable invoice line.
- Included quota switches immediately on upgrade. Already consumed units are not
  reset. Overage is determined against the final effective allowance for this MVP.

## Concurrency and idempotency

Invoice creation locks the subscription row and relies on a unique period key.
Mutations accept `Idempotency-Key`; a reused key with different payload is rejected.
Invoice, audit, and outbox records are written in the same database transaction.

## Payments and webhooks

The sandbox provider signs `timestamp.raw_body` with HMAC-SHA256. Timestamps older
than five minutes are rejected. Provider event IDs are unique, and amount/currency
must exactly match the invoice. A payment state is updated only if the event's
`occurred_at` is newer than the stored provider timestamp, so late/out-of-order events
cannot roll state back. Duplicate delivery returns 200.
