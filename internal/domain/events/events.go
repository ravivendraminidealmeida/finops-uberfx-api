package events

import (
	"time"

	"junglegaming.io/finops-api/internal/domain/ledger"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
)

const EnvelopeVersion = 1

type MoneyAmount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func moneyAmount(m money.Money) MoneyAmount {
	return MoneyAmount{Amount: m.DecimalString(), Currency: m.Currency()}
}

type Envelope struct {
	EventID       string
	EventType     string
	AggregateID   string
	GroupID       string
	CorrelationID string
	CausationID   string
	OccurredAt    time.Time
	Version       int
	Data          any
}

type WagerProcessedData struct {
	TransactionID         string      `json:"transactionId"`
	Kind                  string      `json:"kind"`
	Status                string      `json:"status"`
	WalletID              string      `json:"walletId"`
	PlayerID              string      `json:"playerId"`
	Money                 MoneyAmount `json:"money"`
	ProviderID            string      `json:"providerId,omitempty"`
	ExternalTransactionID string      `json:"externalTransactionId,omitempty"`
	RoundID               string      `json:"roundId,omitempty"`
	GameID                string      `json:"gameId,omitempty"`
}

type WagerRejectedData struct {
	TransactionID         string `json:"transactionId"`
	Kind                  string `json:"kind"`
	FailureCode           string `json:"failureCode"`
	WalletID              string `json:"walletId"`
	ProviderID            string `json:"providerId,omitempty"`
	ExternalTransactionID string `json:"externalTransactionId,omitempty"`
}

type PendingReferenceData struct {
	TransactionID       string `json:"transactionId"`
	ProviderID          string `json:"providerId"`
	ReferenceExternalID string `json:"referenceExternalTransactionId"`
	AttemptCount        int    `json:"attemptCount"`
}

type BalanceChangedData struct {
	WalletID      string      `json:"walletId"`
	TransactionID string      `json:"transactionId"`
	Direction     string      `json:"direction"`
	Money         MoneyAmount `json:"money"`
	BalanceBefore MoneyAmount `json:"balanceBefore"`
	BalanceAfter  MoneyAmount `json:"balanceAfter"`
	WalletVersion int64       `json:"walletVersion"`
}

func newEnvelope(eventID, eventType, aggregateID, groupID, correlationID, causationID string, occurredAt time.Time, data any) Envelope {
	return Envelope{
		EventID:       eventID,
		EventType:     eventType,
		AggregateID:   aggregateID,
		GroupID:       groupID,
		CorrelationID: correlationID,
		CausationID:   causationID,
		OccurredAt:    occurredAt.UTC(),
		Version:       EnvelopeVersion,
		Data:          data,
	}
}

func WagerProcessed(eventID string, tx *wager.Transaction, correlationID string, now time.Time) Envelope {
	data := WagerProcessedData{
		TransactionID: string(tx.ID()),
		Kind:          string(tx.Kind()),
		Status:        string(tx.Status()),
		WalletID:      string(tx.WalletID()),
		PlayerID:      string(tx.PlayerID()),
		Money:         moneyAmount(tx.Amount()),
	}
	if tx.Origin() == wager.OriginExternal {
		data.ProviderID = tx.ProviderID()
		data.ExternalTransactionID = tx.ExternalTransactionID()
		data.RoundID = tx.RoundID()
		data.GameID = tx.GameID()
	}
	return newEnvelope(eventID, "WagerTransactionProcessed", string(tx.ID()), string(tx.WalletID()), correlationID, "", now, data)
}

func WagerRejected(eventID string, tx *wager.Transaction, correlationID string, now time.Time) Envelope {
	data := WagerRejectedData{
		TransactionID: string(tx.ID()),
		Kind:          string(tx.Kind()),
		FailureCode:   string(tx.FailureCode()),
		WalletID:      string(tx.WalletID()),
	}
	if tx.Origin() == wager.OriginExternal {
		data.ProviderID = tx.ProviderID()
		data.ExternalTransactionID = tx.ExternalTransactionID()
	}
	return newEnvelope(eventID, "WagerTransactionRejected", string(tx.ID()), string(tx.WalletID()), correlationID, "", now, data)
}

func PendingReference(eventID string, tx *wager.Transaction, correlationID string, now time.Time) Envelope {
	return newEnvelope(eventID, "WagerTransactionPendingReference", string(tx.ID()), string(tx.WalletID()), correlationID, "", now, PendingReferenceData{
		TransactionID:       string(tx.ID()),
		ProviderID:          tx.ProviderID(),
		ReferenceExternalID: tx.ReferenceExternalID(),
		AttemptCount:        tx.AttemptCount(),
	})
}

func BalanceChanged(eventID string, entry *ledger.Entry, txID wager.ID, walletVersion int64, correlationID string, now time.Time) Envelope {
	wid := string(entry.WalletID())
	return newEnvelope(eventID, "WalletBalanceChanged", wid, wid, correlationID, string(txID), now, BalanceChangedData{
		WalletID:      wid,
		TransactionID: string(txID),
		Direction:     string(entry.Direction()),
		Money:         moneyAmount(entry.Amount()),
		BalanceBefore: moneyAmount(entry.BalanceBefore()),
		BalanceAfter:  moneyAmount(entry.BalanceAfter()),
		WalletVersion: walletVersion,
	})
}
