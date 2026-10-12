package sqsadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.uber.org/zap"

	"junglegaming.io/finops-api/internal/adapter/wire"
	"junglegaming.io/finops-api/internal/app"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
	"junglegaming.io/finops-api/internal/platform/config"
	"junglegaming.io/finops-api/internal/platform/obs"
)

const ConsumerName = "wager-transactions-consumer"

type Client struct {
	api           *sqs.Client
	wagerQueueURL string
	dlqURL        string
	eventsURL     string
	waitSeconds   int32
	visibility    int32
}

func NewClient(ctx context.Context, cfg config.Config) (*Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.AWSRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AWSAccessKey, cfg.AWSSecretKey, "")),
	)
	if err != nil {
		return nil, err
	}
	api := sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.AWSEndpoint != "" {
			o.BaseEndpoint = aws.String(cfg.AWSEndpoint)
		}
	})
	c := &Client{api: api, waitSeconds: 10, visibility: 60}
	c.wagerQueueURL, err = ensureFIFO(ctx, api, cfg.WagerQueueName, cfg.DLQName)
	if err != nil {
		return nil, err
	}
	c.dlqURL, err = queueURL(ctx, api, cfg.DLQName)
	if err != nil {
		return nil, err
	}
	c.eventsURL, err = ensureFIFO(ctx, api, cfg.EventsQueueName, "")
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) SetReceiveOptions(wait, visibility int32) {
	c.waitSeconds = wait
	c.visibility = visibility
}

func (c *Client) Ready(ctx context.Context) error {
	_, err := c.api.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(c.wagerQueueURL),
		AttributeNames: []types.QueueAttributeName{
			types.QueueAttributeNameApproximateNumberOfMessages,
		},
	})
	return err
}

func (c *Client) Publish(ctx context.Context, groupID, eventID string, body []byte) error {
	_, err := c.api.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(c.eventsURL),
		MessageBody:            aws.String(string(body)),
		MessageGroupId:         aws.String(groupID),
		MessageDeduplicationId: aws.String(eventID),
	})
	return err
}

func (c *Client) Receive(ctx context.Context, max int32) ([]types.Message, error) {
	out, err := c.api.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(c.wagerQueueURL),
		MaxNumberOfMessages: max,
		WaitTimeSeconds:     c.waitSeconds,
		VisibilityTimeout:   c.visibility,
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{
			types.MessageSystemAttributeNameMessageGroupId,
		},
	})
	if err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func (c *Client) Delete(ctx context.Context, receipt string) error {
	_, err := c.api.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.wagerQueueURL),
		ReceiptHandle: aws.String(receipt),
	})
	return err
}

func (c *Client) ReceiveEvents(ctx context.Context) ([]types.Message, error) {
	out, err := c.api.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(c.eventsURL),
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     1,
		VisibilityTimeout:   30,
	})
	if err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func (c *Client) ReceiveDLQ(ctx context.Context) ([]types.Message, error) {
	out, err := c.api.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(c.dlqURL),
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     1,
		VisibilityTimeout:   30,
	})
	if err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func (c *Client) SendWager(ctx context.Context, body []byte, group, dedup string) error {
	_, err := c.api.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(c.wagerQueueURL),
		MessageBody:            aws.String(string(body)),
		MessageGroupId:         aws.String(group),
		MessageDeduplicationId: aws.String(dedup),
	})
	return err
}

type Consumer struct {
	client  *Client
	svc     *app.Service
	log     *zap.Logger
	metrics *obs.Metrics
}

func NewConsumer(client *Client, svc *app.Service, log *zap.Logger, metrics *obs.Metrics) *Consumer {
	return &Consumer{client: client, svc: svc, log: log, metrics: metrics}
}

func (c *Consumer) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		msgs, err := c.client.Receive(ctx, 5)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			c.log.Warn("sqs receive failed", zap.Error(err))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			continue
		}
		for _, m := range msgs {
			if err := c.handle(ctx, m); err != nil {
				c.log.Warn("sqs handle failed", zap.Error(err), zap.String("messageId", aws.ToString(m.MessageId)))
			}
		}
	}
}

type inboundEnvelope struct {
	MessageID  string          `json:"messageId"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

type inboundData struct {
	ProviderID            string     `json:"providerId"`
	ExternalTransactionID string     `json:"externalTransactionId"`
	IdempotencyKey        string     `json:"idempotencyKey"`
	PlayerID              string     `json:"playerId"`
	WalletID              string     `json:"walletId"`
	RoundID               string     `json:"roundId"`
	GameID                string     `json:"gameId"`
	Kind                  string     `json:"kind"`
	Money                 wire.Money `json:"money"`
	ReferenceExternalID   string     `json:"referenceExternalTransactionId"`
}

func (c *Consumer) handle(ctx context.Context, m types.Message) error {
	body := aws.ToString(m.Body)
	var env inboundEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		c.metrics.DLQ.Inc()
		c.log.Error("invalid envelope left for redrive", zap.Error(err), zap.String("messageId", aws.ToString(m.MessageId)))
		return err
	}
	msgID := env.MessageID
	if msgID == "" {
		msgID = aws.ToString(m.MessageId)
	}
	sum := sha256.Sum256([]byte(body))
	payloadHash := hex.EncodeToString(sum[:])

	var data inboundData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		c.metrics.DLQ.Inc()
		c.log.Error("invalid payload left for redrive", zap.Error(err), zap.String("messageId", msgID))
		return err
	}
	amt, err := data.Money.Money()
	if err != nil {
		c.metrics.DLQ.Inc()
		return err
	}
	kind, err := wager.ParseKind(data.Kind)
	if err != nil {
		c.metrics.DLQ.Inc()
		return err
	}
	cmd := app.ProcessWagerCommand{
		ProviderID:            data.ProviderID,
		ExternalTransactionID: data.ExternalTransactionID,
		IdempotencyKey:        data.IdempotencyKey,
		PlayerID:              wallet.PlayerID(data.PlayerID),
		WalletID:              wallet.ID(data.WalletID),
		RoundID:               data.RoundID,
		GameID:                data.GameID,
		Kind:                  kind,
		Amount:                amt,
		ReferenceExternalID:   data.ReferenceExternalID,
		AuthenticatedProvider: data.ProviderID,
		Inbox: &app.InboxMeta{
			ConsumerName: ConsumerName,
			MessageID:    msgID,
			PayloadHash:  payloadHash,
		},
	}
	started := time.Now()
	res, err := c.svc.ProcessWager(ctx, cmd)
	c.metrics.ProcessingSeconds.Observe(time.Since(started).Seconds())
	if err != nil {
		c.metrics.Retries.Inc()
		c.log.Warn("sqs wager failed; message kept for retry/DLQ",
			zap.Error(err),
			zap.String("messageId", msgID),
			zap.String("walletId", data.WalletID),
			zap.String("providerId", data.ProviderID),
		)
		return err
	}
	c.metrics.Results.WithLabelValues(string(res.Transaction.Status()), string(res.Transaction.Kind())).Inc()
	if res.IdempotentReplay {
		c.metrics.Duplicates.Inc()
	}
	c.log.Info("sqs wager processed",
		zap.String("messageId", msgID),
		zap.String("correlationId", string(res.Transaction.ID())),
		zap.String("transactionId", string(res.Transaction.ID())),
		zap.String("walletId", data.WalletID),
		zap.String("providerId", data.ProviderID),
	)
	return c.client.Delete(ctx, aws.ToString(m.ReceiptHandle))
}

type Publisher struct {
	uow        app.UnitOfWork
	client     *Client
	instanceID string
	clock      app.Clock
	log        *zap.Logger
	metrics    *obs.Metrics
}

func NewPublisher(uow app.UnitOfWork, client *Client, instanceID string, clock app.Clock, log *zap.Logger, metrics *obs.Metrics) *Publisher {
	return &Publisher{uow: uow, client: client, instanceID: instanceID, clock: clock, log: log, metrics: metrics}
}

func (p *Publisher) Run(ctx context.Context) error {
	delay := 200 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
			n, err := p.tick(ctx)
			if err != nil && ctx.Err() == nil {
				p.log.Warn("outbox tick failed", zap.Error(err))
			}
			if n == 0 {
				delay *= 2
				if delay > 2*time.Second {
					delay = 2 * time.Second
				}
			} else {
				delay = 200 * time.Millisecond
			}
		}
	}
}

func (p *Publisher) tick(ctx context.Context) (int, error) {
	now := p.clock.Now()
	tx, err := p.uow.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recs, err := tx.Outbox().Claim(ctx, p.instanceID, 30*time.Second, 20, now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	var published []string
	if len(recs) > 0 {
		lag := now.Sub(recs[0].OccurredAt).Seconds()
		if lag < 0 {
			lag = 0
		}
		p.metrics.OutboxLagSeconds.Set(lag)
	}
	for _, rec := range recs {
		if err := p.client.Publish(ctx, rec.GroupID, rec.EventID, rec.Payload); err != nil {
			rtx, berr := p.uow.Begin(ctx)
			if berr != nil {
				return len(published), berr
			}
			if rerr := rtx.Outbox().Retry(ctx, rec.ID, rec.Attempts, now.Add(time.Duration(rec.Attempts)*time.Second)); rerr != nil {
				_ = rtx.Rollback(ctx)
				return len(published), rerr
			}
			if err := rtx.Commit(ctx); err != nil {
				return len(published), err
			}
			continue
		}
		published = append(published, rec.ID)
	}
	if len(published) > 0 {
		rtx, berr := p.uow.Begin(ctx)
		if berr != nil {
			return 0, berr
		}
		if err := rtx.Outbox().MarkPublished(ctx, published, now); err != nil {
			_ = rtx.Rollback(ctx)
			return 0, err
		}
		if err := rtx.Commit(ctx); err != nil {
			return 0, err
		}
	}
	return len(recs), nil
}

type ReferenceWorker struct {
	svc *app.Service
	log *zap.Logger
}

func NewReferenceWorker(svc *app.Service, log *zap.Logger) *ReferenceWorker {
	return &ReferenceWorker{svc: svc, log: log}
}

func (w *ReferenceWorker) Run(ctx context.Context) error {
	delay := 200 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
			n, err := w.svc.RetryPendingReferences(ctx, 20)
			if err != nil && ctx.Err() == nil {
				w.log.Warn("pending reference retry failed", zap.Error(err))
			}
			if n == 0 {
				delay *= 2
				if delay > 2*time.Second {
					delay = 2 * time.Second
				}
			} else {
				delay = 200 * time.Millisecond
			}
		}
	}
}

func ensureFIFO(ctx context.Context, api *sqs.Client, name, dlqName string) (string, error) {
	attrs := map[string]string{
		"FifoQueue":                 "true",
		"ContentBasedDeduplication": "false",
	}
	if dlqName != "" {
		dlqURL, err := ensureFIFO(ctx, api, dlqName, "")
		if err != nil {
			return "", err
		}
		arnOut, err := api.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       aws.String(dlqURL),
			AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
		})
		if err != nil {
			return "", err
		}
		attrs["RedrivePolicy"] = fmt.Sprintf(`{"deadLetterTargetArn":"%s","maxReceiveCount":"5"}`, arnOut.Attributes["QueueArn"])
	}
	out, err := api.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String(name),
		Attributes: attrs,
	})
	if err != nil {
		return queueURL(ctx, api, name)
	}
	return aws.ToString(out.QueueUrl), nil
}

func queueURL(ctx context.Context, api *sqs.Client, name string) (string, error) {
	out, err := api.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.QueueUrl), nil
}
