.PHONY: env up dev observability down clean logs proto lint test test-race build e2e
env:            ## generate .env with random secrets
	sh scripts/gen-env.sh
up:             ## build and start the core stack
	docker compose up -d --build
dev:            ## core stack + published infra ports + Kafka UI
	docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
observability:  ## core stack + Prometheus (127.0.0.1:9091) + Grafana (127.0.0.1:3001)
	docker compose -f docker-compose.yml -f docker-compose.observability.yml up -d --build
down:
	docker compose down
clean:
	docker compose down -v
logs:
	docker compose logs -f $(SERVICE)
proto:
	buf lint && buf generate
build:
	go build ./...
test:
	go test ./...
test-race:      ## requires cgo (runs natively on Linux/macOS/CI)
	go test -race ./...
lint:
	go vet ./... && gofmt -l pkg services
e2e:            ## end-to-end tests against the running stack (reads admin login from .env)
	set -a; . ./.env; set +a; VENDORA_ADMIN_EMAIL="$$ADMIN_EMAIL" VENDORA_ADMIN_PASSWORD="$$ADMIN_PASSWORD" go test -tags e2e -count=1 -v ./tests/e2e
