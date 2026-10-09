# finops-uberfx-api

Go service (Uber Fx) for provider wagering: HTTP + SQS, PostgreSQL ledger, Keycloak, LocalStack.

Go version: see `go.mod` (Dockerfile uses Go 1.24).

## Prerequisites

- Docker / Docker Compose
- Go 1.24+
- `golang-migrate` is optional; `go run ./cmd/migrate` is enough

## Local environment

Copy [`.env.example`](.env.example). Values are local fixtures, not production secrets.

```sh
docker compose up --build
```

This starts PostgreSQL, LocalStack (SQS), Keycloak (`http://localhost:8081`) and the API (`http://localhost:8080`). The API applies migrations on boot and creates:

- `wager-transactions.fifo` (redrive to DLQ after 5 receives)
- `wager-transactions-dlq.fifo`
- `domain-events.fifo`

### Run the API on the host

```sh
docker compose up postgres localstack keycloak
export $(grep -v '^#' .env.example | xargs)
go run ./cmd/api
```

Issuer from the host is `http://localhost:8081/realms/finops`. Inside Compose the API uses `http://keycloak:8080/realms/finops`.

## Migrations

```sh
go run ./cmd/migrate up
go run ./cmd/migrate down   # rolls back the last version
```

SQL lives in `internal/adapter/postgres/migrations` (embedded) and is mirrored under `migrations/`.

## Identities (Keycloak)

| client            | secret                            | role                         |
|-------------------|-----------------------------------|------------------------------|
| `provider-a`      | `NU_TEST_provider-a-secret`       | provider `provider-a`        |
| `provider-b`      | `NU_TEST_provider-b-secret`       | provider `provider-b`        |
| `wallet-internal` | `NU_TEST_wallet-internal-secret`  | `POST /wallets` only         |

```sh
# host port 8081 — use http://keycloak:8080 from Compose
TOKEN=$(curl -s -X POST http://localhost:8081/realms/finops/protocol/openid-connect/token \
  -d grant_type=client_credentials -d client_id=wallet-internal -d client_secret=NU_TEST_wallet-internal-secret \
  | python -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')

curl -s -X POST http://localhost:8080/wallets \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","initialBalance":{"amount":"1000.00","currency":"BRL"}}'
```

Provider wagering:

```sh
PTOKEN=$(curl -s -X POST http://localhost:8081/realms/finops/protocol/openid-connect/token \
  -d grant_type=client_credentials -d client_id=provider-a -d client_secret=NU_TEST_provider-a-secret \
  | python -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')

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

Health: `GET /health/live`, `GET /health/ready`. Metrics: `GET /metrics`.

HTTP outcomes: `400` invalid input, `401` missing/invalid token, `403` wrong provider or internal-only route, `409` idempotency/wallet conflict, `422` business rejection (`failureCode` on the body), `202` `PENDING_REFERENCE`, `503` not ready.

## Tests

```sh
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
```

Unit tests cover `Money`, wallet invariants, wager transitions, ledger checks and canonical hashes.

Integration (PostgreSQL, LocalStack; Docker required):

```sh
# Colima / nu docker: Testcontainers needs the socket. Ryuk is disabled in TestMain.
export DOCKER_HOST=unix://$HOME/.colima/default/docker.sock
go test -tags=integration -race -count=1 ./internal/integration/...
```

That suite covers the 80/80 race, 50 duplicate bets, parallel wallets, pending `REFUND` then resolve, provider isolation, SQS inbox, outbox publish, and Fx start/stop.

Multi-instance: `docker compose up --scale api=3` (set distinct `INSTANCE_ID` if you override the compose file). Crash after commit / before SQS delete is covered by redelivery + inbox replay.

## Layout

See `ARCHITECTURE.md` for money, locks, inbox/outbox, auth and Fx.
