.PHONY: up down migrate seed test integration load lint

up:
	docker compose up --build

down:
	docker compose down -v

migrate:
	docker compose run --rm api /meterflow migrate

seed:
	docker compose run --rm api /meterflow seed

test:
	docker compose run --rm api go test ./...

integration:
	docker compose --profile test run --rm integration

load:
	docker compose --profile load run --rm k6 run /scripts/usage.js

lint:
	docker compose run --rm api go vet ./...
