COMPOSE=docker compose -f docker-compose.yaml
SOURCE?=all

.PHONY: tidy build up down migrate-up sync-indices indices-scheduler

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
