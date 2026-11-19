# Tucano Indices

Standalone Go backend for Brazilian financial market indexes and the economic series used as market references. The service has no user accounts or roles; one configured API token protects its API.

## Run locally

1. Set `API_TOKEN` in `.env` (or export it in the shell).
2. Start PostgreSQL, run the migration, and start the API:

```sh
docker compose up --build -d api
```

Compose runs the database migration before the API starts. The API listens on `http://localhost:8011`; health is available at `/healthz`, the interactive Swagger UI at `/swagger`, and the OpenAPI file at `/openapi.yaml`.

Run a one-shot sync or start the long-running scheduler:

```sh
make sync-indices SOURCE=all
make indices-scheduler
```

For a historical backfill, pass a date window:

```sh
make sync-indices SOURCE=b3 ARGS="-from 1998-01-01 -to 2026-11-19"
```

The scheduler runs on weekdays in `America/Sao_Paulo`: Focus at 07:00, Tesouro at 18:30, SGS at 19:30, observed Selic at 20:30, and B3 indexes at 21:00. Each worker is also available as a one-shot command and through the token-protected operations API.

## API

The full OpenAPI 3.1 contract is in [`api/openapi.yaml`](api/openapi.yaml). Service and worker runbooks, including their data flow diagrams, are indexed in [`docs/`](docs/README.md).

Send the token as `Authorization: Bearer <API_TOKEN>`.

| Method | Endpoint | Purpose |
|---|---|---|
| `GET` | `/v1/indices` | Browse the catalog; supports `group`, comma-separated `codes`, and `latest=true` |
| `GET` | `/v1/indices/{code}/final` | Latest published point (or latest point per dimension) |
| `GET` | `/v1/indices/{code}/series` | Ascending chart points; supports `from`, `to`, `last`, `dim`, `as_of`, and `resolution=raw\|weekly\|monthly` |
| `PUT` | `/v1/indices/{code}/observations` | Idempotent manual import or correction; requires `Idempotency-Key` |
| `POST` | `/v1/operations/sync/{source}` | Trigger `all`, `sgs`, `focus`, `tesouro`, `b3`, or `selic` |
| `GET` | `/v1/operations/sync-runs` | Read the sync ledger |

Chart points have a compact `{ "x": "YYYY-MM-DD", "y": "decimal" }` shape, with optional `dimension`, `source`, and `attributes`. Database indexes support date-ordered lookups by series and dimension. Weekly and monthly resolutions retain the last observation in each period.

The former read paths `/v1/market-metrics` and `/v1/market-metrics/{code}` remain as compatibility aliases. The detailed Selic routes and CSV import are also retained under `/v1/selic`.

## Catalog coverage

The original catalog is retained: Selic target/effective/observed, CDI, DI curve, TJLP, NTN-B real yield, IPCA, IPCA-15, trailing IPCA 12-month, IGP-M, Focus IPCA/Selic/USD expectations, PTAX, Ibovespa, IDIV, IFIX, and IBC-Br. INPC and additional B3 market breadth and sector indexes have been added.

New B3 series include IBrX 100/50, IBrA, Small Cap, MidLarge Cap, IFNC, ICON, IMAT, IMOB, UTIL, IEEX, INDX, IGCX, and IGCT. See the seeded catalog in `db/migrations/00001_indices.sql` for stable codes, units, dimensions, and source mappings.

The DI curve remains in the catalog for compatibility with the source service. Its prior worker documented that the upstream metrics API did not publish DI curves, so this service does not claim to ingest that curve. NTN-B yields use the free Tesouro Transparente feed by default.

## Sources and revisions

- Banco Central SGS and Focus: public time-series and expectations APIs.
- BCB NovoSelic: daily official observed-rate data, including statistical fields.
- Tesouro Transparente: public Tesouro Direto rates CSV for real-yield observations.
- B3: public official daily index-statistics feed for B3 index values.

Each stored point is keyed by series, dimension, and reference date. Identical values are no-ops; corrected values create a new revision and retain the previous revision. Workers use PostgreSQL advisory locks and record source, outcome, range, and point counts in `index_sync_runs`.

Relevant source references:

- [BCB open data and SGS](https://dadosabertos.bcb.gov.br/)
- [BCB market expectations](https://dadosabertos.bcb.gov.br/dataset/expectativas-mercado)
- [B3 index statistics](https://sistemaswebb3-listados.b3.com.br/indexStatisticsPage/daily-evolution/IBOVESPA?language=pt-br)
- [Tesouro Transparente dataset](https://www.tesourotransparente.gov.br/ckan/dataset/taxas-dos-titulos-ofertados-pelo-tesouro-direto)
