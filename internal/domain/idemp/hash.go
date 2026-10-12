package idemp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Payload is the business snapshot hashed for idempotency.
// Transport headers (Idempotency-Key, SQS messageId) are intentionally absent.
type Payload struct {
	ProviderID            string
	ExternalTransactionID string
	PlayerID              string
	WalletID              string
	RoundID               string
	GameID                string
	Kind                  string
	Amount                string
	Currency              string
	ReferenceExternalID   string
}

func (p Payload) Hash() (string, error) {
	fields := map[string]any{
		"providerId":            p.ProviderID,
		"externalTransactionId": p.ExternalTransactionID,
		"playerId":              p.PlayerID,
		"walletId":              p.WalletID,
		"roundId":               p.RoundID,
		"gameId":                p.GameID,
		"kind":                  p.Kind,
		"money": map[string]any{
			"amount":   p.Amount,
			"currency": p.Currency,
		},
	}
	if p.ReferenceExternalID != "" {
		fields["referenceExternalTransactionId"] = p.ReferenceExternalID
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
