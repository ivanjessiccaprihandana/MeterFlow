# Load test protocol and results

The k6 scenario drives 50 usage-ingestion requests/second for 30 seconds with up to
100 VUs. It asserts less than 1% failures and p95 below 250 ms. Run it against a
fresh seeded Compose stack:

```sh
cp .env.example .env
docker compose up -d --build postgres redis
docker compose run --rm api migrate
docker compose run --rm api seed
docker compose up -d api worker
docker compose --profile load run --rm k6 run --summary-export=/scripts/results/latest.json /scripts/usage.js
```

## Measured result

Not measured on the current host: Docker and Go were unavailable on 2026-10-10.
No throughput or latency number is claimed. `results/latest.json` is intentionally
absent until a real run produces it. The CI workflow verifies correctness but does
not pretend its shared runner is a stable benchmark environment.

A real local microbenchmark of overflow-safe invoice arithmetic was possible after a
workspace-local Go 1.24.6 toolchain was downloaded. On Windows/amd64 (AMD Ryzen 7
6800H), three runs measured 304.1, 299.9, and 303.9 ns/op, each at 232 B/op and 9
allocations/op. This measures only integer-money arithmetic and is explicitly **not**
presented as an end-to-end API load result. PostgreSQL/Redis/k6 measurements remain
pending because Docker is unavailable on this host.
