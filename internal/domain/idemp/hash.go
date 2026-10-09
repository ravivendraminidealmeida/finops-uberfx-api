package idemp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

func CanonicalHash(fields map[string]any) (string, error) {
	canonical, err := canonicalize(fields)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalize(v any) ([]byte, error) {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			kb, _ := json.Marshal(k)
			vb, err := canonicalize(val[k])
			if err != nil {
				return nil, err
			}
			buf = append(buf, kb...)
			buf = append(buf, ':')
			buf = append(buf, vb...)
		}
		buf = append(buf, '}')
		return buf, nil
	case []any:
		buf := []byte{'['}
		for i, item := range val {
			if i > 0 {
				buf = append(buf, ',')
			}
			ib, err := canonicalize(item)
			if err != nil {
				return nil, err
			}
			buf = append(buf, ib...)
		}
		buf = append(buf, ']')
		return buf, nil
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("canonicalize: %w", err)
		}
		return b, nil
	}
}

func BusinessFields(providerID, externalID, playerID, walletID, roundID, gameID, kind, amount, currency, referenceExternalID string) map[string]any {
	fields := map[string]any{
		"providerId":            providerID,
		"externalTransactionId": externalID,
		"playerId":              playerID,
		"walletId":              walletID,
		"roundId":               roundID,
		"gameId":                gameID,
		"kind":                  kind,
		"money": map[string]any{
			"amount":   amount,
			"currency": currency,
		},
	}
	if referenceExternalID != "" {
		fields["referenceExternalTransactionId"] = referenceExternalID
	}
	return fields
}
