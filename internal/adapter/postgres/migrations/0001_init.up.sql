CREATE TABLE wallets (
    id UUID PRIMARY KEY,
    player_id UUID NOT NULL,
    currency CHAR(3) NOT NULL,
    balance_minor BIGINT NOT NULL CHECK (balance_minor >= 0),
    version BIGINT NOT NULL CHECK (version >= 1),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (player_id, currency)
);

CREATE TABLE wager_transactions (
    id UUID PRIMARY KEY,
    origin TEXT NOT NULL CHECK (origin IN ('INTERNAL', 'EXTERNAL')),
    kind TEXT NOT NULL CHECK (kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
    status TEXT NOT NULL CHECK (status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')),
    wallet_id UUID NOT NULL REFERENCES wallets (id),
    player_id UUID NOT NULL,
    money_minor BIGINT NOT NULL,
    currency CHAR(3) NOT NULL,
    provider_id TEXT,
    external_transaction_id TEXT,
    idempotency_key TEXT,
    payload_hash TEXT,
    round_id TEXT,
    game_id TEXT,
    reference_external_transaction_id TEXT,
    referenced_transaction_id UUID REFERENCES wager_transactions (id),
    failure_code TEXT,
    result_balance_minor BIGINT,
    attempt_count INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    processed_at TIMESTAMPTZ,
    CONSTRAINT external_fields CHECK (
        (origin = 'INTERNAL' AND kind = 'OPENING' AND provider_id IS NULL AND external_transaction_id IS NULL AND idempotency_key IS NULL)
        OR
        (origin = 'EXTERNAL' AND kind <> 'OPENING' AND provider_id IS NOT NULL AND external_transaction_id IS NOT NULL AND idempotency_key IS NOT NULL AND payload_hash IS NOT NULL)
    )
);

CREATE UNIQUE INDEX wager_external_id_uq ON wager_transactions (provider_id, external_transaction_id)
    WHERE origin = 'EXTERNAL';

CREATE UNIQUE INDEX wager_idempotency_uq ON wager_transactions (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE UNIQUE INDEX wager_opening_uq ON wager_transactions (wallet_id)
    WHERE kind = 'OPENING';

CREATE UNIQUE INDEX wager_active_reversal_uq ON wager_transactions (referenced_transaction_id, kind)
    WHERE kind IN ('REFUND', 'ROLLBACK')
      AND referenced_transaction_id IS NOT NULL
      AND status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED');

CREATE INDEX wager_pending_ref_idx ON wager_transactions (next_attempt_at)
    WHERE status = 'PENDING_REFERENCE';

CREATE TABLE wallet_ledger (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL REFERENCES wallets (id),
    transaction_id UUID NOT NULL REFERENCES wager_transactions (id),
    direction TEXT NOT NULL CHECK (direction IN ('DEBIT', 'CREDIT')),
    money_minor BIGINT NOT NULL CHECK (money_minor > 0),
    currency CHAR(3) NOT NULL,
    balance_before_minor BIGINT NOT NULL,
    balance_after_minor BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (wallet_id, transaction_id),
    CHECK (
        (direction = 'CREDIT' AND balance_after_minor = balance_before_minor + money_minor)
        OR
        (direction = 'DEBIT' AND balance_after_minor = balance_before_minor - money_minor)
    )
);

CREATE INDEX wallet_ledger_wallet_created_idx ON wallet_ledger (wallet_id, created_at, id);

CREATE OR REPLACE FUNCTION forbid_ledger_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'wallet_ledger is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER wallet_ledger_no_update
    BEFORE UPDATE OR DELETE ON wallet_ledger
    FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();

CREATE TABLE inbox (
    consumer_name TEXT NOT NULL,
    message_id TEXT NOT NULL,
    payload_hash TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (consumer_name, message_id)
);

CREATE TABLE outbox (
    id UUID PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    event_type TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    locked_until TIMESTAMPTZ,
    locked_by TEXT
);

CREATE INDEX outbox_claim_idx ON outbox (next_attempt_at)
    WHERE published_at IS NULL;
