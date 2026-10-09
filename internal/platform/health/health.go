package health

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	sqsadapter "junglegaming.io/finops-api/internal/adapter/sqs"
)

type Checker struct {
	pool *pgxpool.Pool
	sqs  *sqsadapter.Client
}

func New(pool *pgxpool.Pool, sqs *sqsadapter.Client) *Checker {
	return &Checker{pool: pool, sqs: sqs}
}

func (c *Checker) Ready(ctx context.Context) error {
	if err := c.pool.Ping(ctx); err != nil {
		return err
	}
	return c.sqs.Ready(ctx)
}
