package ledger

import (
	"time"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

type Direction string

const (
	Debit  Direction = "DEBIT"
	Credit Direction = "CREDIT"
)

type ID string

type Entry struct {
	id            ID
	walletID      wallet.ID
	transactionID string
	direction     Direction
	amount        money.Money
	balanceBefore money.Money
	balanceAfter  money.Money
	createdAt     time.Time
}

func (d Direction) apply(before, amount money.Money) (money.Money, error) {
	switch d {
	case Credit:
		return before.Add(amount)
	case Debit:
		return before.Sub(amount)
	default:
		return money.Money{}, domain.ErrUninitialized
	}
}

func New(id ID, walletID wallet.ID, transactionID string, direction Direction, amount, before, after money.Money, now time.Time) (*Entry, error) {
	if id == "" || walletID == "" || transactionID == "" {
		return nil, domain.ErrUninitialized
	}
	if !amount.IsPositive() {
		return nil, domain.ErrInvalidAmount
	}
	expected, err := direction.apply(before, amount)
	if err != nil {
		return nil, err
	}
	eq, err := expected.Equal(after)
	if err != nil {
		return nil, err
	}
	if !eq {
		return nil, domain.ErrLedgerInvariant
	}
	return &Entry{
		id:            id,
		walletID:      walletID,
		transactionID: transactionID,
		direction:     direction,
		amount:        amount,
		balanceBefore: before,
		balanceAfter:  after,
		createdAt:     now.UTC(),
	}, nil
}

func Rehydrate(id ID, walletID wallet.ID, transactionID string, direction Direction, amount, before, after money.Money, createdAt time.Time) (*Entry, error) {
	return New(id, walletID, transactionID, direction, amount, before, after, createdAt)
}

func (e *Entry) ID() ID                     { return e.id }
func (e *Entry) WalletID() wallet.ID        { return e.walletID }
func (e *Entry) TransactionID() string      { return e.transactionID }
func (e *Entry) Direction() Direction       { return e.direction }
func (e *Entry) Amount() money.Money        { return e.amount }
func (e *Entry) BalanceBefore() money.Money { return e.balanceBefore }
func (e *Entry) BalanceAfter() money.Money  { return e.balanceAfter }
func (e *Entry) CreatedAt() time.Time       { return e.createdAt }
