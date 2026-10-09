package app

import (
	"context"
	"time"

	"junglegaming.io/finops-api/internal/domain/ledger"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

type Clock interface {
	Now() time.Time
}

type IDGen interface {
	New() string
}

type UnitOfWork interface {
	Begin(ctx context.Context) (Tx, error)
}

type Tx interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	Wallets() WalletRepo
	Wagers() WagerRepo
	Ledger() LedgerRepo
	Inbox() InboxRepo
	Outbox() OutboxRepo
}

type WalletRepo interface {
	Insert(ctx context.Context, w *wallet.Wallet) error
	Get(ctx context.Context, id wallet.ID) (*wallet.Wallet, error)
	GetByPlayerCurrency(ctx context.Context, playerID wallet.PlayerID, currency string) (*wallet.Wallet, error)
	ApplyDebit(ctx context.Context, id wallet.ID, amount money.Money, now time.Time) (before, after money.Money, version int64, err error)
	ApplyCredit(ctx context.Context, id wallet.ID, amount money.Money, now time.Time) (before, after money.Money, version int64, err error)
}

type WagerRepo interface {
	Insert(ctx context.Context, tx *wager.Transaction) error
	Get(ctx context.Context, id wager.ID) (*wager.Transaction, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*wager.Transaction, error)
	GetByExternalID(ctx context.Context, providerID, externalID string) (*wager.Transaction, error)
	LockByID(ctx context.Context, id wager.ID) (*wager.Transaction, error)
	LockByExternalID(ctx context.Context, providerID, externalID string) (*wager.Transaction, error)
	Update(ctx context.Context, tx *wager.Transaction) error
	ListPendingReferences(ctx context.Context, now time.Time, limit int) ([]*wager.Transaction, error)
	HasActiveReversal(ctx context.Context, referenced wager.ID, kind wager.Kind, except wager.ID) (bool, error)
}

type LedgerRepo interface {
	Insert(ctx context.Context, e *ledger.Entry) error
	ListByWallet(ctx context.Context, walletID wallet.ID, afterCreatedAt time.Time, afterID string, limit int) ([]*ledger.Entry, error)
	SumByWallet(ctx context.Context, walletID wallet.ID) (credits, debits int64, count int, err error)
}

type InboxRepo interface {
	Insert(ctx context.Context, consumerName, messageID, payloadHash string, now time.Time) (inserted bool, existingHash string, err error)
	Complete(ctx context.Context, consumerName, messageID string, now time.Time) error
}

type OutboxRepo interface {
	Insert(ctx context.Context, rec OutboxRecord) error
	Claim(ctx context.Context, instanceID string, lockFor time.Duration, limit int, now time.Time) ([]OutboxRecord, error)
	MarkPublished(ctx context.Context, id string, now time.Time) error
	Retry(ctx context.Context, id string, attempts int, next time.Time) error
}

type OutboxRecord struct {
	ID          string
	EventID     string
	EventType   string
	AggregateID string
	Payload     []byte
	OccurredAt  time.Time
	Attempts    int
	NextAttempt time.Time
	PublishedAt *time.Time
}

type EventPublisher interface {
	Publish(ctx context.Context, body []byte) error
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
