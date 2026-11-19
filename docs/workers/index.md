# Workers

Cada worker sincroniza as séries do catálogo cuja origem corresponde à sua fonte. Todos usam o mesmo `Syncer`, compartilham os advisory locks por fonte e registram a execução no ledger.

| Worker | Origem no catálogo | Séries principais |
| --- | --- | --- |
| [SGS](/workers/sgs) | `bcb_sgs:<código>` | Selic, CDI, IPCA, IGP-M, PTAX e IBC-Br. |
| [Focus](/workers/focus) | `bcb_focus` | Expectativas de IPCA, Selic e câmbio. |
| [Tesouro](/workers/tesouro) | `tesouro_transparente` | Juro real NTN-B por vencimento. |
| [B3](/workers/b3) | `b3_statistics:<sigla>` | Índices de ações, fundos e setores. |
| [Selic](/workers/selic) | `bcb_novoselic` | Selic apurada e estatísticas diárias. |

## Agenda

O [scheduler](/operacao/scheduler) executa os workers em dias úteis no fuso `America/Sao_Paulo`:

| Fonte | Horário |
| --- | ---: |
| Focus | 07:00 |
| Tesouro | 18:30 |
| SGS | 19:30 |
| Selic apurada | 20:30 |
| B3 | 21:00 |

Cada worker também pode ser disparado manualmente pelo CLI ou pelas rotas de operação da [API](/api).
