package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"junglegaming.io/finops-api/internal/adapter/wire"
	"junglegaming.io/finops-api/internal/app"
)

type inboxRepo struct{ q querier }

func (r *inboxRepo) Insert(ctx context.Context, consumerName, messageID, payloadHash string, now time.Time) (inserted bool, existingHash string, err error) {
	err = r.q.QueryRow(ctx, `
		INSERT INTO inbox (consumer_name, message_id, payload_hash, received_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (consumer_name, message_id) DO NOTHING
		RETURNING payload_hash
	`, consumerName, messageID, payloadHash, now.UTC()).Scan(&existingHash)
	if err == nil {
		return true, payloadHash, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, "", err
	}
	err = r.q.QueryRow(ctx, `
		SELECT payload_hash FROM inbox WHERE consumer_name = $1 AND message_id = $2
	`, consumerName, messageID).Scan(&existingHash)
	return false, existingHash, err
}

func (r *inboxRepo) Complete(ctx context.Context, consumerName, messageID string, now time.Time) error {
	_, err := r.q.Exec(ctx, `
		UPDATE inbox SET completed_at = $3
		 WHERE consumer_name = $1 AND message_id = $2 AND completed_at IS NULL
	`, consumerName, messageID, now.UTC())
	return err
}

type outboxRepo struct{ q querier }

func (r *outboxRepo) Insert(ctx context.Context, rec app.OutboxRecord) error {
	payload, err := wire.MarshalEnvelope(rec.Envelope)
	if err != nil {
		return err
	}
	agg, err := uuid.Parse(rec.AggregateID)
	if err != nil {
		return err
	}
	eventID, err := uuid.Parse(rec.EventID)
	if err != nil {
		return err
	}
	rowID, err := uuid.Parse(rec.ID)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `
		INSERT INTO outbox (id, event_id, event_type, aggregate_id, message_group_id, payload, occurred_at, attempts, next_attempt_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8)
	`, rowID, eventID, rec.EventType, agg, rec.GroupID, payload, rec.OccurredAt, rec.NextAttempt)
	return err
}

func (r *outboxRepo) Claim(ctx context.Context, instanceID string, lockFor time.Duration, limit int, now time.Time) ([]app.OutboxRecord, error) {
	rows, err := r.q.Query(ctx, `
		UPDATE outbox
		   SET locked_until = $2,
		       locked_by = $1,
		       attempts = attempts + 1
		 WHERE id IN (
		   SELECT id FROM outbox
		    WHERE published_at IS NULL
		      AND next_attempt_at <= $3
		      AND (locked_until IS NULL OR locked_until < $3)
		    ORDER BY occurred_at
		    LIMIT $4
		    FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id, event_id, event_type, aggregate_id, message_group_id, payload, occurred_at, attempts, next_attempt_at
	`, instanceID, now.Add(lockFor), now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.OutboxRecord
	for rows.Next() {
		var rec app.OutboxRecord
		var id, eventID, agg uuid.UUID
		if err := rows.Scan(&id, &eventID, &rec.EventType, &agg, &rec.GroupID, &rec.Payload, &rec.OccurredAt, &rec.Attempts, &rec.NextAttempt); err != nil {
			return nil, err
		}
		rec.ID = id.String()
		rec.EventID = eventID.String()
		rec.AggregateID = agg.String()
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *outboxRepo) MarkPublished(ctx context.Context, ids []string, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	uids := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		u, err := uuid.Parse(id)
		if err != nil {
			return err
		}
		uids = append(uids, u)
	}
	_, err := r.q.Exec(ctx, `UPDATE outbox SET published_at = $2, locked_until = NULL WHERE id = ANY($1)`, uids, now.UTC())
	return err
}

func (r *outboxRepo) Retry(ctx context.Context, id string, attempts int, next time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE outbox SET attempts = $2, next_attempt_at = $3, locked_until = NULL WHERE id = $1`, id, attempts, next.UTC())
	return err
}
