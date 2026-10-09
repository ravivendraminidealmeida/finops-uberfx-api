package wager

import (
	"fmt"
	"time"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

type Kind string
type Status string
type Origin string
type ID string

const (
	KindOpening  Kind = "OPENING"
	KindBet      Kind = "BET"
	KindWin      Kind = "WIN"
	KindLoss     Kind = "LOSS"
	KindRefund   Kind = "REFUND"
	KindRollback Kind = "ROLLBACK"
)

const (
	StatusPending          Status = "PENDING"
	StatusPendingReference Status = "PENDING_REFERENCE"
	StatusProcessed        Status = "PROCESSED"
	StatusRejected         Status = "REJECTED"
	StatusFailed           Status = "FAILED"
)

const (
	OriginInternal Origin = "INTERNAL"
	OriginExternal Origin = "EXTERNAL"
)

type Transaction struct {
	id                    ID
	origin                Origin
	kind                  Kind
	status                Status
	walletID              wallet.ID
	playerID              wallet.PlayerID
	amount                money.Money
	providerID            string
	externalTransactionID string
	idempotencyKey        string
	payloadHash           string
	roundID               string
	gameID                string
	referenceExternalID   string
	referencedInternalID  ID
	failureCode           FailureCode
	resultBalanceMinor    *int64
	attemptCount          int
	nextAttemptAt         *time.Time
	createdAt             time.Time
	updatedAt             time.Time
	processedAt           *time.Time
}

type NewExternalParams struct {
	ID                    ID
	Kind                  Kind
	WalletID              wallet.ID
	PlayerID              wallet.PlayerID
	Amount                money.Money
	ProviderID            string
	ExternalTransactionID string
	IdempotencyKey        string
	PayloadHash           string
	RoundID               string
	GameID                string
	ReferenceExternalID   string
	Now                   time.Time
}

func NewExternal(p NewExternalParams) (*Transaction, error) {
	if p.ID == "" || p.WalletID == "" || p.PlayerID == "" || p.ProviderID == "" || p.ExternalTransactionID == "" {
		return nil, domain.ErrUninitialized
	}
	if p.IdempotencyKey == "" || p.PayloadHash == "" || p.RoundID == "" || p.GameID == "" {
		return nil, domain.ErrUninitialized
	}
	if !p.Amount.Initialized() {
		return nil, domain.ErrUninitialized
	}
	if p.Kind == KindOpening {
		return nil, domain.ErrOpeningNotAllowed
	}
	if err := validateExternalAmount(p.Kind, p.Amount); err != nil {
		return nil, err
	}
	if (p.Kind == KindRefund || p.Kind == KindRollback) && p.ReferenceExternalID == "" {
		return nil, domain.ErrReferenceRequired
	}
	now := p.Now.UTC()
	return &Transaction{
		id:                    p.ID,
		origin:                OriginExternal,
		kind:                  p.Kind,
		status:                StatusPending,
		walletID:              p.WalletID,
		playerID:              p.PlayerID,
		amount:                p.Amount,
		providerID:            p.ProviderID,
		externalTransactionID: p.ExternalTransactionID,
		idempotencyKey:        p.IdempotencyKey,
		payloadHash:           p.PayloadHash,
		roundID:               p.RoundID,
		gameID:                p.GameID,
		referenceExternalID:   p.ReferenceExternalID,
		createdAt:             now,
		updatedAt:             now,
	}, nil
}

func NewOpening(id ID, walletID wallet.ID, playerID wallet.PlayerID, amount money.Money, now time.Time) (*Transaction, error) {
	if id == "" || walletID == "" || playerID == "" || !amount.Initialized() {
		return nil, domain.ErrUninitialized
	}
	if !amount.IsPositive() {
		return nil, domain.ErrInvalidAmount
	}
	t := now.UTC()
	processed := t
	minor := amount.Minor()
	return &Transaction{
		id:                 id,
		origin:             OriginInternal,
		kind:               KindOpening,
		status:             StatusProcessed,
		walletID:           walletID,
		playerID:           playerID,
		amount:             amount,
		resultBalanceMinor: &minor,
		createdAt:          t,
		updatedAt:          t,
		processedAt:        &processed,
	}, nil
}

func Rehydrate(t Transaction) (*Transaction, error) {
	if t.id == "" || t.walletID == "" || t.playerID == "" || !t.amount.Initialized() {
		return nil, domain.ErrUninitialized
	}
	return &t, nil
}

func validateExternalAmount(kind Kind, amount money.Money) error {
	switch kind {
	case KindLoss:
		if !amount.IsZero() {
			return fmt.Errorf("%w: LOSS requires 0.00", domain.ErrInvalidAmount)
		}
	case KindBet, KindWin, KindRefund, KindRollback:
		if !amount.IsPositive() {
			return fmt.Errorf("%w: %s requires amount > 0", domain.ErrInvalidAmount, kind)
		}
	default:
		return domain.ErrInvalidKind
	}
	return nil
}

func (t *Transaction) ID() ID                        { return t.id }
func (t *Transaction) Origin() Origin                { return t.origin }
func (t *Transaction) Kind() Kind                    { return t.kind }
func (t *Transaction) Status() Status                { return t.status }
func (t *Transaction) WalletID() wallet.ID           { return t.walletID }
func (t *Transaction) PlayerID() wallet.PlayerID     { return t.playerID }
func (t *Transaction) Amount() money.Money           { return t.amount }
func (t *Transaction) ProviderID() string            { return t.providerID }
func (t *Transaction) ExternalTransactionID() string { return t.externalTransactionID }
func (t *Transaction) IdempotencyKey() string        { return t.idempotencyKey }
func (t *Transaction) PayloadHash() string           { return t.payloadHash }
func (t *Transaction) RoundID() string               { return t.roundID }
func (t *Transaction) GameID() string                { return t.gameID }
func (t *Transaction) ReferenceExternalID() string   { return t.referenceExternalID }
func (t *Transaction) ReferencedInternalID() ID      { return t.referencedInternalID }
func (t *Transaction) FailureCode() FailureCode      { return t.failureCode }
func (t *Transaction) ResultBalanceMinor() *int64    { return t.resultBalanceMinor }
func (t *Transaction) AttemptCount() int             { return t.attemptCount }
func (t *Transaction) NextAttemptAt() *time.Time     { return t.nextAttemptAt }
func (t *Transaction) CreatedAt() time.Time          { return t.createdAt }
func (t *Transaction) UpdatedAt() time.Time          { return t.updatedAt }
func (t *Transaction) ProcessedAt() *time.Time       { return t.processedAt }

func (t *Transaction) IsTerminal() bool {
	return t.status == StatusProcessed || t.status == StatusRejected || t.status == StatusFailed
}

func (t *Transaction) MovesMoney() bool {
	switch t.kind {
	case KindBet, KindWin, KindRefund, KindRollback, KindOpening:
		return true
	default:
		return false
	}
}

func (t *Transaction) DebitDirection() bool {
	switch t.kind {
	case KindBet:
		return true
	case KindRollback:
		return t.referencedWasCredit()
	default:
		return false
	}
}

func (t *Transaction) referencedWasCredit() bool {
	return false
}

func (t *Transaction) MarkProcessed(balance money.Money, referenced ID, now time.Time) error {
	if t.IsTerminal() {
		return domain.ErrTerminalTransaction
	}
	if t.status != StatusPending && t.status != StatusPendingReference {
		return domain.ErrInvalidStateTransition
	}
	t.status = StatusProcessed
	t.referencedInternalID = referenced
	minor := balance.Minor()
	t.resultBalanceMinor = &minor
	ts := now.UTC()
	t.processedAt = &ts
	t.updatedAt = ts
	t.nextAttemptAt = nil
	return nil
}

func (t *Transaction) MarkRejected(code FailureCode, now time.Time) error {
	if t.IsTerminal() {
		return domain.ErrTerminalTransaction
	}
	if t.status != StatusPending && t.status != StatusPendingReference {
		return domain.ErrInvalidStateTransition
	}
	t.status = StatusRejected
	t.failureCode = code
	ts := now.UTC()
	t.updatedAt = ts
	t.processedAt = &ts
	t.nextAttemptAt = nil
	return nil
}

func (t *Transaction) MarkFailed(code FailureCode, now time.Time) error {
	if t.IsTerminal() {
		return domain.ErrTerminalTransaction
	}
	t.status = StatusFailed
	t.failureCode = code
	ts := now.UTC()
	t.updatedAt = ts
	t.processedAt = &ts
	t.nextAttemptAt = nil
	return nil
}

func (t *Transaction) MarkPendingReference(attempt int, next time.Time, now time.Time) error {
	if t.IsTerminal() {
		return domain.ErrTerminalTransaction
	}
	if t.status != StatusPending && t.status != StatusPendingReference {
		return domain.ErrInvalidStateTransition
	}
	t.status = StatusPendingReference
	t.attemptCount = attempt
	n := next.UTC()
	t.nextAttemptAt = &n
	t.updatedAt = now.UTC()
	return nil
}

func (t *Transaction) SetReferenced(id ID) {
	t.referencedInternalID = id
}

func ParseKind(s string) (Kind, error) {
	k := Kind(s)
	switch k {
	case KindOpening, KindBet, KindWin, KindLoss, KindRefund, KindRollback:
		return k, nil
	default:
		return "", domain.ErrInvalidKind
	}
}
