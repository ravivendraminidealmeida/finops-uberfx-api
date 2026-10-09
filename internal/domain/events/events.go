package events

import (
	"time"

	"junglegaming.io/finops-api/internal/domain/ledger"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

const EnvelopeVersion = 1

type Envelope struct {
	EventID       string         `json:"eventId"`
	EventType     string         `json:"eventType"`
	AggregateID   string         `json:"aggregateId"`
	CorrelationID string         `json:"correlationId"`
	CausationID   string         `json:"causationId,omitempty"`
	OccurredAt    time.Time      `json:"occurredAt"`
	Version       int            `json:"version"`
	Data          map[string]any `json:"data"`
}

func NewEnvelope(eventID, eventType, aggregateID, correlationID, causationID string, occurredAt time.Time, data map[string]any) Envelope {
	return Envelope{
		EventID:       eventID,
		EventType:     eventType,
		AggregateID:   aggregateID,
		CorrelationID: correlationID,
		CausationID:   causationID,
		OccurredAt:    occurredAt.UTC(),
		Version:       EnvelopeVersion,
		Data:          data,
	}
}

func WagerProcessed(eventID string, tx *wager.Transaction, correlationID string, now time.Time) Envelope {
	data := map[string]any{
		"transactionId": string(tx.ID()),
		"kind":          string(tx.Kind()),
		"status":        string(tx.Status()),
		"walletId":      string(tx.WalletID()),
		"playerId":      string(tx.PlayerID()),
		"money":         moneyDTO(tx.Amount()),
	}
	if tx.Origin() == wager.OriginExternal {
		data["providerId"] = tx.ProviderID()
		data["externalTransactionId"] = tx.ExternalTransactionID()
		data["roundId"] = tx.RoundID()
		data["gameId"] = tx.GameID()
	}
	return NewEnvelope(eventID, "WagerTransactionProcessed", string(tx.ID()), correlationID, "", now, data)
}

func WagerRejected(eventID string, tx *wager.Transaction, correlationID string, now time.Time) Envelope {
	data := map[string]any{
		"transactionId": string(tx.ID()),
		"kind":          string(tx.Kind()),
		"failureCode":   string(tx.FailureCode()),
		"walletId":      string(tx.WalletID()),
	}
	if tx.Origin() == wager.OriginExternal {
		data["providerId"] = tx.ProviderID()
		data["externalTransactionId"] = tx.ExternalTransactionID()
	}
	return NewEnvelope(eventID, "WagerTransactionRejected", string(tx.ID()), correlationID, "", now, data)
}

func PendingReference(eventID string, tx *wager.Transaction, correlationID string, now time.Time) Envelope {
	return NewEnvelope(eventID, "WagerTransactionPendingReference", string(tx.ID()), correlationID, "", now, map[string]any{
		"transactionId":                  string(tx.ID()),
		"providerId":                     tx.ProviderID(),
		"referenceExternalTransactionId": tx.ReferenceExternalID(),
		"attemptCount":                   tx.AttemptCount(),
	})
}

func BalanceChanged(eventID string, entry *ledger.Entry, txID wager.ID, walletVersion int64, correlationID string, now time.Time) Envelope {
	return NewEnvelope(eventID, "WalletBalanceChanged", string(entry.WalletID()), correlationID, string(txID), now, map[string]any{
		"walletId":      string(entry.WalletID()),
		"transactionId": string(txID),
		"direction":     string(entry.Direction()),
		"money":         moneyDTO(entry.Amount()),
		"balanceBefore": moneyDTO(entry.BalanceBefore()),
		"balanceAfter":  moneyDTO(entry.BalanceAfter()),
		"walletVersion": walletVersion,
	})
}

func moneyDTO(m money.Money) map[string]string {
	return map[string]string{
		"amount":   m.DecimalString(),
		"currency": m.Currency(),
	}
}

func WalletID(id wallet.ID) string { return string(id) }
