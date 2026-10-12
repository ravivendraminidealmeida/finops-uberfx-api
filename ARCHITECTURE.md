# Arquitetura

## Dinheiro

`Money` é um value object imutável: `int64` em unidades mínimas (centavos) mais código ISO 4217. O domínio fala `Parse` / `DecimalString` / `FromMinor`; não implementa `json.Marshaler`. HTTP/SQS mapeiam `{"amount":"25.00","currency":"BRL"}` no adapter (`wire.Money`). O parse exige exatamente duas casas, rejeita `NaN` / `Infinity` / notação científica / zeros à esquerda / escala extra, e verifica overflow em parse, soma, subtração e negação. Persistência é `BIGINT` + `CHAR(3)`. Nenhum `float32`/`float64` no caminho. Aritmética exige moedas iguais. Entradas externas não podem ser negativas; diferenças internas podem.

O hash de idempotência usa a string decimal canônica depois de um parse válido (`25.00`), depois SHA-256 de `encoding/json` sobre um `map` de campos de negócio (Go ordena as chaves; Idempotency-Key e metadados de transporte SQS ficam de fora). `ProcessWager` calcula o hash; HTTP e SQS compartilham o mesmo `idemp.Payload`.

## Transações e unidade de trabalho

Uma única transação PostgreSQL (`ReadCommitted`) cobre, conforme o caso: insert/complete da inbox, linha da aposta, `UPDATE` da carteira por versão, insert do ledger, inserts da outbox. Eventos nunca são publicados dentro dessa transação. `pgx` com SQL explícito; os repositórios recebem o mesmo `pgx.Tx`. Reconciliação usa `RepeatableRead` para ler saldo e soma do ledger no mesmo snapshot.

## Concorrência (híbrida)

A linha da carteira é lida com `SELECT ... FOR UPDATE` **antes** da busca de idempotência. Débito e crédito acontecem no agregado (`Wallet.Debit` / `Credit`) e persistem com:

```
UPDATE wallets SET balance_minor = $after, version = $new, updated_at = $now
 WHERE id = $id AND version = $expected AND currency = $cur
```

`CHECK (balance_minor >= 0)` é a última linha de defesa. `version` começa em 1 e só incrementa quando o saldo muda.

`REFUND`/`ROLLBACK` fazem `SELECT ... FOR UPDATE` na aposta referenciada, depois `HasActiveReversal` e unique em `referenced_transaction_id`. Carteiras independentes nunca compartilham lock.

Atualização perdida não atravessa um write commitado: o `FOR UPDATE` serializa escritores da mesma carteira, inclusive entre processos.

## Idempotência

Uniques persistentes: `(provider_id, external_transaction_id)` para operações externas; `(provider_id, idempotency_key)`; `(wallet_id)` para `OPENING`. Mesma chave + mesmo hash devolve o resultado gravado e `idempotentReplay=true`, inclusive o `result_balance_minor` original. Mesma chave + hash diferente é HTTP 409. Mesmo id externo com outra chave é 409. Insert da inbox usa `ON CONFLICT DO NOTHING`.

## Referências

Referências ausentes ou ainda pendentes persistem `PENDING_REFERENCE` com backoff exponencial (`PENDING_REF_BACKOFF_MS * 2^(attempt-1)`) e teto de tentativas. A inbox pode completar assim que a linha pendente é durável; o worker de referência retoma. Esgotamento → `REJECTED` / `REFERENCE_NOT_FOUND`. Referência existente `REJECTED`/`FAILED` → `REFERENCE_NOT_PROCESSED`. `REFUND` só se aplica a `BET` processada. `ROLLBACK` se aplica a `BET`/`WIN`/`REFUND` processadas. `WIN` aceita referência opcional a uma `BET` (valor do prêmio pode diferir da aposta; não entra no unique de reversão). `REFUND` e `ROLLBACK` do mesmo BET são mutuamente exclusivos (`DUPLICATE_REVERSAL`). `ROLLBACK` de um `REFUND` debita o reembolso. Débito de reversão que estouraria o saldo é `INSUFFICIENT_FUNDS_REVERSAL`.

## Inbox / outbox / SQS

- Entrada FIFO `wager-transactions.fifo` com DLQ `wager-transactions-dlq.fifo`, `maxReceiveCount=5`, visibilidade 60s, long poll 10s.
- `MessageGroupId` = `walletId`. `MessageDeduplicationId` = `messageId` do envelope.
- Delete da mensagem SQS só depois do commit de um tratamento durável (sucesso ou rejeição de negócio persistida). JSON inválido, kind/money inválidos e erros de aplicação **não** apagam a mensagem; o redrive leva à DLQ.
- Outbox reivindicada com `FOR UPDATE SKIP LOCKED`; publicação em `domain-events.fifo` com `MessageGroupId` = id da carteira.

Eventos usam structs concretas (`WagerProcessedData`, `BalanceChangedData`, …) no campo `data` do envelope.

## Autenticação

Realm Keycloak `finops`, `client_credentials`. Clients `provider-a` / `provider-b` emitem JWT com `provider_id` e audience `finops-api`. `wallet-internal` é a única identidade permitida em carteira, ledger e reconciliação. O middleware verifica assinatura/expiração/audience via JWKS. Sem o claim `provider_id`, 401. Divergência de corpo/path é 403. Sem armazenamento de senha nem emissão local de token.

## Uber Fx

Módulos: `config`, `postgres`, `sqs`, `auth`, `app`, `http`, `workers`. Pacotes de domínio não importam Fx. O stop cancela o contexto dos workers e espera, faz `Shutdown` do HTTP e depois fecha o pool.

## Observabilidade

Logs JSON com `correlationId`, `messageId`, `transactionId`, `walletId` e `providerId` (sem payload financeiro). Métricas Prometheus: resultados por status, duplicatas, retries, candidatos a DLQ, conflitos, atraso da outbox, latência de processamento e divergências de reconciliação.

## Encerramento

`SIGTERM` → Fx `OnStop`: workers param de receber, HTTP `Shutdown`. Mensagens SQS em voo que não terminam mantêm visibilidade.

## Limitações

- Só BRL é exercitado nos testes; o tipo ainda carrega a moeda.
- Destino da outbox é SQS FIFO, não SNS/EventBridge.
- `FAILED` fica reservado a infraestrutura permanente gravada explicitamente.
- O teste de “três instâncias” usa três `Service`/pools no mesmo processo (conexões e heaps independentes). Processos OS separados ficam no `docker compose --scale`.
