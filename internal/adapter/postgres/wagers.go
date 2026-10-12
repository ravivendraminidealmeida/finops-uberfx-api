package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
)

type wagerRepo struct{ q querier }

func (r *wagerRepo) Insert(ctx context.Context, tx *wager.Transaction) error {
	_, err := r.q.Exec(ctx, wagerInsertSQL, wagerArgs(tx)...)
	return mapPGErr(err)
}

func (r *wagerRepo) Update(ctx context.Context, tx *wager.Transaction) error {
	ct, err := r.q.Exec(ctx, `
		UPDATE wager_transactions SET
			status = $2,
			referenced_transaction_id = $3,
			failure_code = $4,
			result_balance_minor = $5,
			attempt_count = $6,
			next_attempt_at = $7,
			updated_at = $8,
			processed_at = $9
		WHERE id = $1
	`, tx.ID(), tx.Status(), nilIfEmpty(string(tx.ReferencedInternalID())), nilIfEmpty(string(tx.FailureCode())),
		resultMinor(tx), tx.AttemptCount(), nilTime(tx.NextAttemptAt()), tx.UpdatedAt(), nilTime(tx.ProcessedAt()))
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return domain.ErrTransactionNotFound
	}
	return nil
}

func (r *wagerRepo) Get(ctx context.Context, id wager.ID) (*wager.Transaction, error) {
	return r.scan(r.q.QueryRow(ctx, wagerSelect+" WHERE id = $1", id), domain.ErrTransactionNotFound)
}

func (r *wagerRepo) LockByID(ctx context.Context, id wager.ID) (*wager.Transaction, error) {
	return r.scan(r.q.QueryRow(ctx, wagerSelect+" WHERE id = $1 FOR UPDATE", id), domain.ErrTransactionNotFound)
}

func (r *wagerRepo) GetByIdempotencyKey(ctx context.Context, providerID, key string) (*wager.Transaction, error) {
	return r.scan(r.q.QueryRow(ctx, wagerSelect+" WHERE provider_id = $1 AND idempotency_key = $2", providerID, key), domain.ErrTransactionNotFound)
}

func (r *wagerRepo) GetByExternalID(ctx context.Context, providerID, externalID string) (*wager.Transaction, error) {
	return r.scan(r.q.QueryRow(ctx, wagerSelect+" WHERE provider_id = $1 AND external_transaction_id = $2", providerID, externalID), domain.ErrTransactionNotFound)
}

func (r *wagerRepo) LockByExternalID(ctx context.Context, providerID, externalID string) (*wager.Transaction, error) {
	return r.scan(r.q.QueryRow(ctx, wagerSelect+" WHERE provider_id = $1 AND external_transaction_id = $2 FOR UPDATE", providerID, externalID), domain.ErrTransactionNotFound)
}

func (r *wagerRepo) HasActiveReversal(ctx context.Context, referenced wager.ID, except wager.ID) (bool, error) {
	var exists bool
	err := r.q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM wager_transactions
			 WHERE referenced_transaction_id = $1
			   AND kind IN ('REFUND', 'ROLLBACK')
			   AND id <> $2
			   AND status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED')
		)
	`, referenced, except).Scan(&exists)
	return exists, err
}

func (r *wagerRepo) ListPendingReferences(ctx context.Context, now time.Time, limit int) ([]*wager.Transaction, error) {
	rows, err := r.q.Query(ctx, `
		UPDATE wager_transactions
		   SET next_attempt_at = $1 + interval '30 seconds'
		 WHERE id IN (
		   SELECT id FROM wager_transactions
		    WHERE status = 'PENDING_REFERENCE' AND next_attempt_at <= $1
		    ORDER BY next_attempt_at
		    LIMIT $2
		    FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id, origin, kind, status, wallet_id, player_id, money_minor, currency,
		       provider_id, external_transaction_id, idempotency_key, payload_hash,
		       round_id, game_id, reference_external_transaction_id, referenced_transaction_id,
		       failure_code, result_balance_minor, attempt_count, next_attempt_at,
		       created_at, updated_at, processed_at
	`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*wager.Transaction
	for rows.Next() {
		t, err := scanWager(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const wagerSelect = `
SELECT id, origin, kind, status, wallet_id, player_id, money_minor, currency,
       provider_id, external_transaction_id, idempotency_key, payload_hash,
       round_id, game_id, reference_external_transaction_id, referenced_transaction_id,
       failure_code, result_balance_minor, attempt_count, next_attempt_at,
       created_at, updated_at, processed_at
FROM wager_transactions`

const wagerInsertSQL = `
INSERT INTO wager_transactions (
	id, origin, kind, status, wallet_id, player_id, money_minor, currency,
	provider_id, external_transaction_id, idempotency_key, payload_hash,
	round_id, game_id, reference_external_transaction_id, referenced_transaction_id,
	failure_code, result_balance_minor, attempt_count, next_attempt_at,
	created_at, updated_at, processed_at
) VALUES (
	$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23
)`

func wagerArgs(tx *wager.Transaction) []any {
	return []any{
		tx.ID(), tx.Origin(), tx.Kind(), tx.Status(), tx.WalletID(), tx.PlayerID(),
		tx.Amount().Minor(), tx.Amount().Currency(),
		nilIfEmpty(tx.ProviderID()), nilIfEmpty(tx.ExternalTransactionID()),
		nilIfEmpty(tx.IdempotencyKey()), nilIfEmpty(tx.PayloadHash()),
		nilIfEmpty(tx.RoundID()), nilIfEmpty(tx.GameID()),
		nilIfEmpty(tx.ReferenceExternalID()), nilIfEmpty(string(tx.ReferencedInternalID())),
		nilIfEmpty(string(tx.FailureCode())), resultMinor(tx), tx.AttemptCount(),
		nilTime(tx.NextAttemptAt()), tx.CreatedAt(), tx.UpdatedAt(), nilTime(tx.ProcessedAt()),
	}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (r *wagerRepo) scan(row rowScanner, notFound error) (*wager.Transaction, error) {
	t, err := scanWager(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound
	}
	return t, err
}

func scanWager(row rowScanner) (*wager.Transaction, error) {
	var (
		id, origin, kind, status, walletID, playerID, currency             string
		minor                                                              int64
		provider, extID, idem, hash, roundID, gameID, refExt, refInt, fail *string
		resultMinor                                                        *int64
		attempts                                                           int
		nextAttempt, processed                                             *time.Time
		created, updated                                                   time.Time
	)
	if err := row.Scan(
		&id, &origin, &kind, &status, &walletID, &playerID, &minor, &currency,
		&provider, &extID, &idem, &hash, &roundID, &gameID, &refExt, &refInt,
		&fail, &resultMinor, &attempts, &nextAttempt, &created, &updated, &processed,
	); err != nil {
		return nil, err
	}
	amt, err := money.FromMinor(minor, currency)
	if err != nil {
		return nil, err
	}
	return wager.Rehydrate(wager.Snapshot{
		ID: id, Origin: origin, Kind: kind, Status: status, WalletID: walletID, PlayerID: playerID,
		Amount: amt, ProviderID: derefStr(provider), ExternalID: derefStr(extID),
		IdempotencyKey: derefStr(idem), PayloadHash: derefStr(hash),
		RoundID: derefStr(roundID), GameID: derefStr(gameID),
		ReferenceExternalID: derefStr(refExt), ReferencedID: derefStr(refInt),
		FailureCode: derefStr(fail), ResultMinor: resultMinor, AttemptCount: attempts,
		NextAttempt: nextAttempt, Processed: processed, CreatedAt: created, UpdatedAt: updated,
	})
}

func resultMinor(tx *wager.Transaction) any {
	bal, ok := tx.ResultBalance()
	if !ok {
		return nil
	}
	return bal.Minor()
}

func nilTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
