package wallet

import (
	"fmt"
	"time"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
)

type ID string
type PlayerID string

type Wallet struct {
	id        ID
	playerID  PlayerID
	balance   money.Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

func New(id ID, playerID PlayerID, initial money.Money, now time.Time) (*Wallet, error) {
	if id == "" || playerID == "" {
		return nil, domain.ErrUninitialized
	}
	if !initial.Initialized() {
		return nil, domain.ErrUninitialized
	}
	if initial.IsNegative() {
		return nil, domain.ErrInvalidAmount
	}
	return &Wallet{
		id:        id,
		playerID:  playerID,
		balance:   initial,
		version:   1,
		createdAt: now.UTC(),
		updatedAt: now.UTC(),
	}, nil
}

func Rehydrate(id ID, playerID PlayerID, balance money.Money, version int64, createdAt, updatedAt time.Time) (*Wallet, error) {
	if id == "" || playerID == "" || !balance.Initialized() || version < 1 {
		return nil, domain.ErrUninitialized
	}
	if balance.IsNegative() {
		return nil, domain.ErrInvalidAmount
	}
	return &Wallet{
		id:        id,
		playerID:  playerID,
		balance:   balance,
		version:   version,
		createdAt: createdAt.UTC(),
		updatedAt: updatedAt.UTC(),
	}, nil
}

func (w *Wallet) ID() ID               { return w.id }
func (w *Wallet) PlayerID() PlayerID   { return w.playerID }
func (w *Wallet) Balance() money.Money { return w.balance }
func (w *Wallet) Currency() string     { return w.balance.Currency() }
func (w *Wallet) Version() int64       { return w.version }
func (w *Wallet) CreatedAt() time.Time { return w.createdAt }
func (w *Wallet) UpdatedAt() time.Time { return w.updatedAt }

func (w *Wallet) Debit(amount money.Money, now time.Time) (before, after money.Money, err error) {
	if err := w.ensureCompatible(amount); err != nil {
		return money.Money{}, money.Money{}, err
	}
	if !amount.IsPositive() {
		return money.Money{}, money.Money{}, domain.ErrInvalidAmount
	}
	before = w.balance
	cmp, err := w.balance.Cmp(amount)
	if err != nil {
		return money.Money{}, money.Money{}, err
	}
	if cmp < 0 {
		return money.Money{}, money.Money{}, domain.ErrInsufficientFunds
	}
	after, err = w.balance.Sub(amount)
	if err != nil {
		return money.Money{}, money.Money{}, err
	}
	w.applyBalance(after, now)
	return before, after, nil
}

func (w *Wallet) Credit(amount money.Money, now time.Time) (before, after money.Money, err error) {
	if err := w.ensureCompatible(amount); err != nil {
		return money.Money{}, money.Money{}, err
	}
	if !amount.IsPositive() {
		return money.Money{}, money.Money{}, domain.ErrInvalidAmount
	}
	before = w.balance
	after, err = w.balance.Add(amount)
	if err != nil {
		return money.Money{}, money.Money{}, err
	}
	w.applyBalance(after, now)
	return before, after, nil
}

func (w *Wallet) applyBalance(after money.Money, now time.Time) {
	w.balance = after
	w.version++
	w.updatedAt = now.UTC()
}

func (w *Wallet) ensureCompatible(amount money.Money) error {
	if w == nil || w.id == "" || !w.balance.Initialized() {
		return domain.ErrUninitialized
	}
	if !amount.Initialized() {
		return domain.ErrUninitialized
	}
	if amount.Currency() != w.balance.Currency() {
		return fmt.Errorf("%w: %s vs %s", domain.ErrCurrencyMismatch, amount.Currency(), w.balance.Currency())
	}
	return nil
}
