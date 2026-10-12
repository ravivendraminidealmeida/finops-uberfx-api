package app

import (
	"junglegaming.io/finops-api/internal/domain/idemp"
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
	AuthenticatedProvider string
	Inbox                 *InboxMeta
}

func (cmd ProcessWagerCommand) payloadHash() (string, error) {
	return idemp.Payload{
		ProviderID:            cmd.ProviderID,
		ExternalTransactionID: cmd.ExternalTransactionID,
		PlayerID:              string(cmd.PlayerID),
		WalletID:              string(cmd.WalletID),
		RoundID:               cmd.RoundID,
		GameID:                cmd.GameID,
		Kind:                  string(cmd.Kind),
		Amount:                cmd.Amount.DecimalString(),
		Currency:              cmd.Amount.Currency(),
		ReferenceExternalID:   cmd.ReferenceExternalID,
	}.Hash()
}

type ProcessResult struct {
	Transaction      *wager.Transaction
	Balance          money.Money
	IdempotentReplay bool
}

type OpenWalletCommand struct {
	PlayerID wallet.PlayerID
	Initial  money.Money
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
