# Documentação técnica

O site é construído com [VitePress](https://vitepress.dev/). Para rodar localmente:

```sh
make docs
```

O servidor fica em `http://localhost:5181`.

No GitHub Pages, o workflow `.github/workflows/docs-pages.yml` publica o site após alterações em `docs/` mescladas na branch `main`. A URL do projeto é <https://joselarantes.github.io/tucano-indices/>. Para a primeira publicação, configure **Settings → Pages → Build and deployment → Source** como **GitHub Actions**.

| Documento | Serviço / worker |
|---|---|
| [Introdução](introducao.md) | Visão geral, componentes e fluxo de dados. |
| [API](api.md) | Endpoints HTTP, autenticação, formatos e erros. |
| [Sincronização](operacao/index-sync.md) | Orquestrador, CLI, janelas, locks e ledger. |
| [Scheduler](operacao/scheduler.md) | Agenda dos workers e operação contínua. |
| [Persistência](operacao/persistence.md) | Modelo de dados, revisões, idempotência e ledger. |
| [Workers](workers/index.md) | Visão geral das cinco fontes. |
| [Worker SGS](workers/sgs.md) | Séries temporais do Banco Central. |
| [Worker Focus](workers/focus.md) | Expectativas de IPCA, Selic e câmbio. |
| [Worker Tesouro](workers/tesouro.md) | Rendimentos de títulos públicos Tesouro IPCA+. |
| [Worker B3](workers/b3.md) | Índices de ações, fundos e setores. |
| [Worker Selic](workers/selic.md) | Selic apurada e estatísticas do NovoSelic. |

A especificação OpenAPI fica em [`../api/openapi.yaml`](../api/openapi.yaml). Com o serviço ativo, consulte a interface interativa em `/swagger` ou o arquivo publicado em `/openapi.yaml`.
