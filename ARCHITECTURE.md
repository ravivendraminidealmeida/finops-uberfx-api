# Architecture

## Money

`Money` is an immutable value object: `int64` minor units (centavos) plus an ISO 4217 code. External JSON is always `{"amount":"25.00","currency":"BRL"}`. Parsing requires exactly two decimal places, rejects `NaN` / `Infinity` / scientific notation / leading zeros / extra scale, and checks overflow on parse, add, sub and negate. Persistence is `BIGINT` + `CHAR(3)`. No `float32`/`float64` on any path. Arithmetic requires equal currencies. External inputs cannot be negative; internal differences may be.

Idempotency hashes use the canonical decimal string after a successful parse (`25.00`), SHA-256 of key-sorted JSON of business fields (not the Idempotency-Key header or transport metadata). HTTP and SQS share `idemp.BusinessFields`.

## Transactions and unit of work

A single PostgreSQL transaction (`ReadCommitted`) covers, as applicable: inbox insert/complete, wager row, conditional wallet `UPDATE`, ledger insert, outbox inserts. Events are never published inside that transaction. `pgx` with explicit SQL; repositories receive the same `pgx.Tx`.

## Concurrency (hybrid)

Debits:

```
UPDATE wallets SET balance_minor = balance_minor - $amt, version = version + 1
 WHERE id = $id AND currency = $cur AND balance_minor >= $amt
 RETURNING balance_minor, version
```

Zero rows is insufficient funds (or `INSUFFICIENT_FUNDS_REVERSAL` for a debiting `ROLLBACK`). Credits omit the balance predicate. `CHECK (balance_minor >= 0)` is the last line of defence. `version` starts at 1 and increments only when the balance changes.

`REFUND`/`ROLLBACK` take `SELECT ... FOR UPDATE` on the referenced wager row, then `HasActiveReversal` plus unique `(referenced_transaction_id, kind)` for in-flight/processed reversals. Independent wallets never share a lock.

Lost updates cannot skip a committed write: the balance mutation is a single atomic `UPDATE`.

## Idempotency

Persistent uniques: `(provider_id, external_transaction_id)` for external operations; `idempotency_key`; `(wallet_id)` for `OPENING`. Same key + same hash returns the stored result and `idempotentReplay=true`, including the original `result_balance_minor`. Same key + different hash is HTTP 409. Same external id with a different key is 409. A unique race retries as replay when the hash matches.

## References

Missing or still-pending references persist `PENDING_REFERENCE` with exponential backoff (`PENDING_REF_BACKOFF_MS * 2^(attempt-1)`) and a max attempt count. Inbox may complete once the pending row is durable; the reference worker resumes. Exhaustion → `REJECTED` / `REFERENCE_NOT_FOUND`. A reference that exists but is `REJECTED`/`FAILED` → `REFERENCE_NOT_PROCESSED`. `REFUND` applies only to a processed `BET`. `ROLLBACK` applies to processed `BET`/`WIN`/`REFUND`. `REFUND` then `ROLLBACK` of the same bet: refund credits; rollback of the refund debits; a second refund of the same bet is `DUPLICATE_REVERSAL`. A reversal debit that would exceed the balance is `INSUFFICIENT_FUNDS_REVERSAL`, distinct from `INSUFFICIENT_FUNDS`.

## Inbox / outbox / SQS

- Inbound FIFO `wager-transactions.fifo` with DLQ `wager-transactions-dlq.fifo`, `maxReceiveCount=5`, visibility 60s, long poll 10s.
- `MessageGroupId` = `walletId` (broker serialization per wallet; HTTP still races, so SQL remains authoritative).
- `MessageDeduplicationId` = envelope `messageId` (5-minute SQS window only).
- Inbox uniqueness `(consumer_name, message_id)` plus payload hash. Delete SQS message only after commit. Business rejections delete; infrastructure errors do not (redrive → DLQ).
- Outbox claimed with `FOR UPDATE SKIP LOCKED`; publish to `domain-events.fifo` preserving `eventId`; mark published afterwards. Crash between publish and mark republishes the same `eventId`.

## Authentication

Keycloak realm `finops`, `client_credentials`. Clients `provider-a` / `provider-b` mint JWTs with `provider_id` and audience `finops-api`. `wallet-internal` is the only identity allowed to `POST /wallets`. Middleware verifies signature/expiry/audience via JWKS. The token's `provider_id` (or `azp`) is authoritative; body/path mismatch is 403 with no financial effect. SQS access is broker credentials; the consumer still applies domain rules. No password storage or local token minting.

## Uber Fx

Modules: `config`, `postgres`, `sqs`, `auth`, `app`, `http`, `workers`. Domain packages do not import Fx. Lifecycle starts migrations, pool ping, HTTP server, consumer, outbox publisher, reference worker. Stop cancels worker context, shuts down HTTP, then closes the pool.

## Shutdown

`SIGTERM` → Fx `OnStop`: workers stop receiving, HTTP `Shutdown`, in-flight SQS handlers that do not finish keep the message (visibility restores). Outbox locks expire and are taken by another instance.

## Limitations

- Only BRL is exercised in tests; the type still carries currency.
- Access tokens are treated as OIDC JWTs (Keycloak default).
- Outbox destination is SQS FIFO, not SNS/EventBridge.
- `FAILED` is reserved for permanent infrastructure recorded explicitly; most infra errors stay retryable.
