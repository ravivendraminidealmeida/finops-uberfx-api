package app

import (
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

type InboxMeta struct {
	ConsumerName string
	MessageID    string
	PayloadHash  string
}

type ProcessWagerCommand struct {
	ProviderID            string
	ExternalTransactionID string
	IdempotencyKey        string
	PlayerID              wallet.PlayerID
	WalletID              wallet.ID
	RoundID               string
	GameID                string
	Kind                  wager.Kind
	Amount                money.Money
	ReferenceExternalID   string
	PayloadHash           string
	CorrelationID         string
	AuthenticatedProvider string
	Inbox                 *InboxMeta
}

type ProcessResult struct {
	Transaction      *wager.Transaction
	Balance          money.Money
	IdempotentReplay bool
}

type OpenWalletCommand struct {
	PlayerID        wallet.PlayerID
	Initial         money.Money
	InternalService bool
}

type OpenWalletResult struct {
	Wallet *wallet.Wallet
}

type ReconcileResult struct {
	WalletID          wallet.ID
	StoredBalance     money.Money
	CalculatedBalance money.Money
	Difference        money.Money
	Consistent        bool
	CheckedEntries    int
}
