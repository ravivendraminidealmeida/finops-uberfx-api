package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

type walletRepo struct{ q querier }

func (r *walletRepo) Insert(ctx context.Context, w *wallet.Wallet) error {
	_, err := r.q.Exec(ctx, `
		INSERT INTO wallets (id, player_id, currency, balance_minor, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, w.ID(), w.PlayerID(), w.Currency(), w.Balance().Minor(), w.Version(), w.CreatedAt(), w.UpdatedAt())
	return mapPGErr(err)
}

func (r *walletRepo) Get(ctx context.Context, id wallet.ID) (*wallet.Wallet, error) {
	return r.scanWallet(r.q.QueryRow(ctx, `
		SELECT id, player_id, currency, balance_minor, version, created_at, updated_at
		FROM wallets WHERE id = $1
	`, id))
}

func (r *walletRepo) Lock(ctx context.Context, id wallet.ID) (*wallet.Wallet, error) {
	return r.scanWallet(r.q.QueryRow(ctx, `
		SELECT id, player_id, currency, balance_minor, version, created_at, updated_at
		FROM wallets WHERE id = $1 FOR UPDATE
	`, id))
}

func (r *walletRepo) Save(ctx context.Context, w *wallet.Wallet, expectedVersion int64) error {
	tag, err := r.q.Exec(ctx, `
		UPDATE wallets
		   SET balance_minor = $2,
		       version = $3,
		       updated_at = $4
		 WHERE id = $1 AND version = $5 AND currency = $6
	`, w.ID(), w.Balance().Minor(), w.Version(), w.UpdatedAt(), expectedVersion, w.Currency())
	if err != nil {
		return mapPGErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrWalletNotFound
	}
	return nil
}

func (r *walletRepo) scanWallet(row pgx.Row) (*wallet.Wallet, error) {
	var id, playerID, currency string
	var minor, version int64
	var created, updated time.Time
	if err := row.Scan(&id, &playerID, &currency, &minor, &version, &created, &updated); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWalletNotFound
		}
		return nil, err
	}
	bal, err := money.FromMinor(minor, currency)
	if err != nil {
		return nil, err
	}
	return wallet.Rehydrate(wallet.ID(id), wallet.PlayerID(playerID), bal, version, created, updated)
}
