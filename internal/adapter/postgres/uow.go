package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"junglegaming.io/finops-api/internal/app"
	"junglegaming.io/finops-api/internal/domain"
)

type UoW struct {
	pool *pgxpool.Pool
}

func NewUoW(pool *pgxpool.Pool) *UoW {
	return &UoW{pool: pool}
}

func (u *UoW) Begin(ctx context.Context) (app.Tx, error) {
	tx, err := u.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx}, nil
}

func (u *UoW) Pool() *pgxpool.Pool { return u.pool }

type Tx struct {
	tx pgx.Tx
}

func (t *Tx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t *Tx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
func (t *Tx) Wallets() app.WalletRepo            { return &walletRepo{q: t.tx} }
func (t *Tx) Wagers() app.WagerRepo              { return &wagerRepo{q: t.tx} }
func (t *Tx) Ledger() app.LedgerRepo             { return &ledgerRepo{q: t.tx} }
func (t *Tx) Inbox() app.InboxRepo               { return &inboxRepo{q: t.tx} }
func (t *Tx) Outbox() app.OutboxRepo             { return &outboxRepo{q: t.tx} }

type querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func mapPGErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrWalletNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		switch pg.ConstraintName {
		case "wallets_player_id_currency_key":
			return domain.ErrWalletConflict
		case "wager_idempotency_uq":
			return domain.ErrIdempotencyConflict
		case "wager_external_id_uq":
			return domain.ErrExternalIDConflict
		case "wager_active_reversal_uq":
			return domain.ErrDuplicateReversal
		case "wager_opening_uq":
			return domain.ErrWalletConflict
		case "wallet_ledger_wallet_id_transaction_id_key":
			return domain.ErrIdempotencyConflict
		case "inbox_pkey":
			return errInboxExists
		default:
			return fmt.Errorf("unique violation on %s: %w", pg.ConstraintName, err)
		}
	}
	return err
}

var errInboxExists = errors.New("inbox message exists")
