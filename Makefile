COMPOSE=docker compose -f docker-compose.yaml
SOURCE?=all

.PHONY: tidy build up down migrate-up sync-indices indices-scheduler docs docs-dev lint typecheck unit-test test

docs-dev:
	$(COMPOSE) --profile docs up docs

tidy:
	$(COMPOSE) --profile tools run --rm tooling go mod tidy

build:
	$(COMPOSE) build api index-sync index-scheduler

up:
	$(COMPOSE) up --build -d api

down:
	$(COMPOSE) down

migrate-up:
	$(COMPOSE) run --rm migrate

sync-indices:
	INDICES_SYNC_SOURCE=$(SOURCE) $(COMPOSE) --profile indices-sync run --build --rm index-sync $(ARGS)

indices-scheduler:
	$(COMPOSE) --profile indices-scheduler up --build -d index-scheduler

docs:
	$(COMPOSE) --profile docs up --build docs

lint:
	$(COMPOSE) --profile tools run --rm tooling sh -ec 'if [ ! -x /go/bin/golangci-lint ]; then GOBIN=/go/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0; fi; /go/bin/golangci-lint run --timeout=5m --max-same-issues=0 --max-issues-per-linter=0'

typecheck:
	$(COMPOSE) --profile tools run --rm tooling go build ./...
	$(COMPOSE) --profile tools run --rm tooling go vet ./...

unit-test:
	$(COMPOSE) --profile tools run --rm tooling go test -count=1 ./...

test: lint typecheck unit-test
