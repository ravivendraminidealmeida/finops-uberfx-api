package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"junglegaming.io/finops-api/internal/app"
)

type inboxRepo struct{ q querier }

func (r *inboxRepo) Insert(ctx context.Context, consumerName, messageID, payloadHash string, now time.Time) (inserted bool, existingHash string, err error) {
	_, err = r.q.Exec(ctx, `
		INSERT INTO inbox (consumer_name, message_id, payload_hash, received_at)
		VALUES ($1, $2, $3, $4)
	`, consumerName, messageID, payloadHash, now.UTC())
	if err == nil {
		return true, payloadHash, nil
	}
	if errors.Is(mapPGErr(err), errInboxExists) || isUnique(err) {
		err = r.q.QueryRow(ctx, `
			SELECT payload_hash FROM inbox WHERE consumer_name = $1 AND message_id = $2
		`, consumerName, messageID).Scan(&existingHash)
		return false, existingHash, err
	}
	return false, "", err
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
	id := rec.ID
	if id == "" {
		id = uuid.NewString()
	}
	agg, err := uuid.Parse(rec.AggregateID)
	if err != nil {
		return err
	}
	eventID, err := uuid.Parse(rec.EventID)
	if err != nil {
		return err
	}
	rowID, err := uuid.Parse(id)
	if err != nil {
		rowID = uuid.New()
	}
	_, err = r.q.Exec(ctx, `
		INSERT INTO outbox (id, event_id, event_type, aggregate_id, payload, occurred_at, attempts, next_attempt_at)
		VALUES ($1,$2,$3,$4,$5,$6,0,$7)
	`, rowID, eventID, rec.EventType, agg, rec.Payload, rec.OccurredAt, rec.NextAttempt)
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
		 RETURNING id, event_id, event_type, aggregate_id, payload, occurred_at, attempts, next_attempt_at, published_at
	`, instanceID, now.Add(lockFor), now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.OutboxRecord
	for rows.Next() {
		var rec app.OutboxRecord
		var id, eventID, agg uuid.UUID
		if err := rows.Scan(&id, &eventID, &rec.EventType, &agg, &rec.Payload, &rec.OccurredAt, &rec.Attempts, &rec.NextAttempt, &rec.PublishedAt); err != nil {
			return nil, err
		}
		rec.ID = id.String()
		rec.EventID = eventID.String()
		rec.AggregateID = agg.String()
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *outboxRepo) MarkPublished(ctx context.Context, id string, now time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE outbox SET published_at = $2, locked_until = NULL WHERE id = $1`, id, now.UTC())
	return err
}

func (r *outboxRepo) Retry(ctx context.Context, id string, attempts int, next time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE outbox SET attempts = $2, next_attempt_at = $3, locked_until = NULL WHERE id = $1`, id, attempts, next.UTC())
	return err
}

func isUnique(err error) bool {
	mapped := mapPGErr(err)
	return errors.Is(mapped, errInboxExists)
}

func ScanOutbox(_ pgx.Row) {}
