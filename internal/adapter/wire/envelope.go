package wire

import (
	"encoding/json"
	"time"

	"junglegaming.io/finops-api/internal/domain/events"
)

type Envelope struct {
	EventID       string `json:"eventId"`
	EventType     string `json:"eventType"`
	AggregateID   string `json:"aggregateId"`
	CorrelationID string `json:"correlationId"`
	CausationID   string `json:"causationId,omitempty"`
	OccurredAt    string `json:"occurredAt"`
	Version       int    `json:"version"`
	Data          any    `json:"data"`
}

func MarshalEnvelope(env events.Envelope) ([]byte, error) {
	return json.Marshal(Envelope{
		EventID:       env.EventID,
		EventType:     env.EventType,
		AggregateID:   env.AggregateID,
		CorrelationID: env.CorrelationID,
		CausationID:   env.CausationID,
		OccurredAt:    env.OccurredAt.UTC().Format(time.RFC3339Nano),
		Version:       env.Version,
		Data:          env.Data,
	})
}
