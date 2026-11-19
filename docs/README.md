# Documentação técnica

| Documento | Serviço / worker |
|---|---|
| [API](api.md) | Endpoints HTTP, autenticação, formatos e erros. |
| [Sincronização](index-sync.md) | Orquestrador, CLI, janelas, locks e ledger. |
| [Worker SGS](worker-sgs.md) | Séries temporais do Banco Central. |
| [Worker Focus](worker-focus.md) | Expectativas de IPCA, Selic e câmbio. |
| [Worker Tesouro](worker-tesouro.md) | Rendimentos de títulos públicos Tesouro IPCA+. |
| [Worker B3](worker-b3.md) | Índices de ações, fundos e setores. |
| [Worker Selic](worker-selic.md) | Selic apurada e estatísticas do NovoSelic. |
| [Scheduler](scheduler.md) | Agenda dos workers e operação contínua. |
| [Persistência](persistence.md) | Modelo de dados, revisões, idempotência e ledger. |

A especificação OpenAPI fica em [`../api/openapi.yaml`](../api/openapi.yaml). Com o serviço ativo, consulte a interface interativa em `/swagger` ou o arquivo publicado em `/openapi.yaml`.
