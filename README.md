# finops-uberfx-api

Serviço Go (Uber Fx) para apostas de provedor: HTTP + SQS, ledger PostgreSQL, Keycloak e LocalStack.

Versão do Go: ver `go.mod` (o Dockerfile usa Go 1.27).

## Pré-requisitos

- Docker / Docker Compose
- Go 1.27+
- `jq` (só para os exemplos de token abaixo)

## Ambiente local

Copie [`.env.example`](.env.example). Os valores são fixtures locais.

```sh
docker compose up --build
```

Sobe PostgreSQL, LocalStack (SQS), Keycloak (`http://localhost:8081`) e a API (`http://localhost:8080`). A API aplica as migrations na subida e cria:

- `wager-transactions.fifo` (redrive para a DLQ após 5 recebimentos)
- `wager-transactions-dlq.fifo`
- `domain-events.fifo`

### Rodar a API no host

```sh
docker compose up postgres localstack keycloak
export $(grep -v '^#' .env.example | xargs)
go run ./cmd/api
```

Issuer no host: `http://localhost:8081/realms/finops`. Dentro do Compose a API usa `http://keycloak:8080/realms/finops`.

Várias instâncias: `docker compose up --scale api=3` exige um override sem a porta host `8080:8080` (conflito). Cada réplica usa `INSTANCE_ID` = hostname do container.

## Migrations

```sh
export $(grep -v '^#' .env.example | xargs)
go run ./cmd/migrate up
go run ./cmd/migrate down   # desfaz a última versão
```

O SQL fica em `internal/adapter/postgres/migrations` (embutido no binário).

## Identidades (Keycloak)

| client            | secret                 | papel                              |
|-------------------|------------------------|------------------------------------|
| `provider-a`      | `NU_TEST_provider-a`      | provedor `provider-a`              |
| `provider-b`      | `NU_TEST_provider-b`      | provedor `provider-b`              |
| `wallet-internal` | `NU_TEST_wallet-internal` | carteiras, ledger, reconciliação   |

```sh
TOKEN=$(curl -s -X POST http://localhost:8081/realms/finops/protocol/openid-connect/token \
  -d grant_type=client_credentials -d client_id=wallet-internal -d client_secret=NU_TEST_wallet-internal \
  | jq -r .access_token)

curl -s -X POST http://localhost:8080/wallets \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","initialBalance":{"amount":"1000.00","currency":"BRL"}}'
```

Aposta de provedor:

```sh
PTOKEN=$(curl -s -X POST http://localhost:8081/realms/finops/protocol/openid-connect/token \
  -d grant_type=client_credentials -d client_id=provider-a -d client_secret=NU_TEST_provider-a \
  | jq -r .access_token)

curl -s -X POST http://localhost:8080/wagering/transactions \
  -H "Authorization: Bearer $PTOKEN" \
  -H "Idempotency-Key: provider-a:transaction-123" \
  -H 'Content-Type: application/json' \
  -d '{
    "providerId":"provider-a",
    "externalTransactionId":"transaction-123",
    "playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId":"<wallet-id>",
    "roundId":"round-987",
    "gameId":"fortune-chimp",
    "kind":"BET",
    "money":{"amount":"25.00","currency":"BRL"}
  }'
```

Saúde: `GET /health/live`, `GET /health/ready`. Métricas: `GET /metrics`.

Respostas HTTP: `400` entrada inválida, `401` token ausente/inválido/expirado, `403` provedor errado ou rota interna, `409` conflito de idempotência/carteira, `422` rejeição de negócio (`failureCode` no corpo), `202` `PENDING_REFERENCE`, `503` não pronto.

`failureCode` persistidos (definitivos): `INSUFFICIENT_FUNDS`, `INSUFFICIENT_FUNDS_REVERSAL`, `REFERENCE_NOT_FOUND`, `REFERENCE_NOT_PROCESSED`, `REFERENCE_MISMATCH`, `DUPLICATE_REVERSAL`, `INVALID_AMOUNT`. Entrada corrigível (HTTP 400/401/403/409) não grava `failureCode`. `PERMANENT_INFRASTRUCTURE` fica reservado a `FAILED`.

## Testes

```sh
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
```

Os unitários cobrem `Money`, invariantes de carteira, transições de aposta, ledger e hashes canônicos.

Integração (PostgreSQL, Keycloak e LocalStack em containers; Docker obrigatório):

```sh
go test -tags=integration -race -count=1 ./internal/integration/...
```

A suíte cobre a corrida 80/80, 50 apostas duplicadas, carteiras em paralelo, referências pendentes, isolamento de provedor, exclusão REFUND/ROLLBACK, `ROLLBACK` de `REFUND`, `WIN` com referência opcional, três pools/serviços, inbox SQS, publicação da outbox, redrive para DLQ, HTTP autenticado no Keycloak (token ausente/inválido/expirado) e start/stop do Fx.

Crash depois do commit / antes do delete SQS é coberto por reentrega + replay da inbox.
