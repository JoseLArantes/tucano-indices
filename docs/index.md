---
layout: home

hero:
  text: Índices e séries econômicas do mercado brasileiro
  tagline: Backend REST API para catálogo, histórico revisado e sincronização de dados do BCB, Tesouro Direto e B3.
  actions:
    - theme: brand
      text: Começar pela introdução
      link: /introducao
    - theme: alt
      text: Referência da API
      link: /api

features:
  - title: Catálogo unificado
    details: Índices de mercado, indicadores macro e expectativas em um único catálogo com unidade, frequência e origem.
    link: /api
    linkText: Consultar a API
  - title: Sincronização confiável
    details: Locks por fonte, janelas incrementais, ledger de execuções e reprocessamento seguro.
    link: /operacao/index-sync
    linkText: Como funciona
  - title: Histórico revisado
    details: Cada correção cria uma nova revisão sem perder o valor anterior.
    link: /operacao/persistence
    linkText: Modelo de dados
  - title: Cinco fontes
    details: Workers para SGS, Focus, Tesouro Transparente, B3 e NovoSelic.
    link: /workers/
    linkText: Ver workers
---

## Fontes de dados

- **Banco Central** — séries SGS, expectativas Focus e Selic apurada (NovoSelic).
- **Tesouro Transparente** — taxas indicativas dos títulos Tesouro IPCA+.
- **B3** — evolução diária dos índices de ações, fundos e setores.

## Comece por aqui

| Objetivo | Página |
| --- | --- |
| Entender a arquitetura e o fluxo de dados | [Introdução](/introducao) |
| Integrar com os endpoints HTTP | [API](/api) |
| Operar cargas e o scheduler | [Sincronização](/operacao/index-sync) |
| Acompanhar cada fonte | [Workers](/workers/) |
