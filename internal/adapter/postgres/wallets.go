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

func (r *walletRepo) GetByPlayerCurrency(ctx context.Context, playerID wallet.PlayerID, currency string) (*wallet.Wallet, error) {
	return r.scanWallet(r.q.QueryRow(ctx, `
		SELECT id, player_id, currency, balance_minor, version, created_at, updated_at
		FROM wallets WHERE player_id = $1 AND currency = $2
	`, playerID, currency))
}

func (r *walletRepo) ApplyDebit(ctx context.Context, id wallet.ID, amount money.Money, now time.Time) (before, after money.Money, version int64, err error) {
	var afterMinor, ver int64
	var currency string
	err = r.q.QueryRow(ctx, `
		UPDATE wallets
		   SET balance_minor = balance_minor - $2,
		       version = version + 1,
		       updated_at = $3
		 WHERE id = $1
		   AND currency = $4
		   AND balance_minor >= $2
		 RETURNING balance_minor, version, currency
	`, id, amount.Minor(), now.UTC(), amount.Currency()).Scan(&afterMinor, &ver, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return money.Money{}, money.Money{}, 0, domain.ErrInsufficientFunds
	}
	if err != nil {
		return money.Money{}, money.Money{}, 0, err
	}
	after, err = money.FromMinor(afterMinor, currency)
	if err != nil {
		return money.Money{}, money.Money{}, 0, err
	}
	before, err = after.Add(amount)
	if err != nil {
		return money.Money{}, money.Money{}, 0, err
	}
	return before, after, ver, nil
}

func (r *walletRepo) ApplyCredit(ctx context.Context, id wallet.ID, amount money.Money, now time.Time) (before, after money.Money, version int64, err error) {
	var afterMinor, ver int64
	var currency string
	err = r.q.QueryRow(ctx, `
		UPDATE wallets
		   SET balance_minor = balance_minor + $2,
		       version = version + 1,
		       updated_at = $3
		 WHERE id = $1
		   AND currency = $4
		 RETURNING balance_minor, version, currency
	`, id, amount.Minor(), now.UTC(), amount.Currency()).Scan(&afterMinor, &ver, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return money.Money{}, money.Money{}, 0, domain.ErrWalletNotFound
	}
	if err != nil {
		return money.Money{}, money.Money{}, 0, err
	}
	after, err = money.FromMinor(afterMinor, currency)
	if err != nil {
		return money.Money{}, money.Money{}, 0, err
	}
	before, err = after.Sub(amount)
	if err != nil {
		return money.Money{}, money.Money{}, 0, err
	}
	return before, after, ver, nil
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
