package postgres

import (
	"context"
	"time"

	"junglegaming.io/finops-api/internal/domain/ledger"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

type ledgerRepo struct{ q querier }

func (r *ledgerRepo) Insert(ctx context.Context, e *ledger.Entry) error {
	_, err := r.q.Exec(ctx, `
		INSERT INTO wallet_ledger (
			id, wallet_id, transaction_id, direction, money_minor, currency,
			balance_before_minor, balance_after_minor, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, e.ID(), e.WalletID(), e.TransactionID(), e.Direction(), e.Amount().Minor(), e.Amount().Currency(),
		e.BalanceBefore().Minor(), e.BalanceAfter().Minor(), e.CreatedAt())
	return mapPGErr(err)
}

func (r *ledgerRepo) ListByWallet(ctx context.Context, walletID wallet.ID, afterCreatedAt time.Time, afterID string, limit int) ([]*ledger.Entry, error) {
	if afterID == "" {
		afterID = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := r.q.Query(ctx, `
		SELECT id, wallet_id, transaction_id, direction, money_minor, currency,
		       balance_before_minor, balance_after_minor, created_at
		  FROM wallet_ledger
		 WHERE wallet_id = $1
		   AND (created_at, id) > ($2, $3)
		 ORDER BY created_at, id
		 LIMIT $4
	`, walletID, afterCreatedAt, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ledger.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *ledgerRepo) SumByWallet(ctx context.Context, walletID wallet.ID) (credits, debits int64, count int, err error) {
	err = r.q.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN money_minor ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'DEBIT' THEN money_minor ELSE 0 END), 0),
			COUNT(*)
		FROM wallet_ledger
		WHERE wallet_id = $1
	`, walletID).Scan(&credits, &debits, &count)
	return
}

func scanEntry(row rowScanner) (*ledger.Entry, error) {
	var id, walletID, txID, dir, currency string
	var minor, before, after int64
	var created time.Time
	if err := row.Scan(&id, &walletID, &txID, &dir, &minor, &currency, &before, &after, &created); err != nil {
		return nil, err
	}
	amt, err := money.FromMinor(minor, currency)
	if err != nil {
		return nil, err
	}
	b, err := money.FromMinor(before, currency)
	if err != nil {
		return nil, err
	}
	a, err := money.FromMinor(after, currency)
	if err != nil {
		return nil, err
	}
	return ledger.Rehydrate(ledger.ID(id), wallet.ID(walletID), txID, ledger.Direction(dir), amt, b, a, created)
}
