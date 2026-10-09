package sqsadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.uber.org/zap"

	"junglegaming.io/finops-api/internal/app"
	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/idemp"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
	"junglegaming.io/finops-api/internal/platform/config"
)

const ConsumerName = "wager-transactions-consumer"

type Client struct {
	api           *sqs.Client
	wagerQueueURL string
	eventsURL     string
	dlqURL        string
}

func NewClient(ctx context.Context, cfg config.Config) (*Client, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.AWSRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AWSAccessKey, cfg.AWSSecretKey, "")),
	}
	if cfg.AWSEndpoint != "" {
		loadOpts = append(loadOpts, awsconfig.WithBaseEndpoint(cfg.AWSEndpoint))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}
	api := sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.AWSEndpoint != "" {
			o.BaseEndpoint = aws.String(cfg.AWSEndpoint)
		}
	})
	c := &Client{api: api}
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

func (c *Client) Ready(ctx context.Context) error {
	_, err := c.api.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(c.wagerQueueURL),
		AttributeNames: []types.QueueAttributeName{
			types.QueueAttributeNameApproximateNumberOfMessages,
		},
	})
	return err
}

func (c *Client) Publish(ctx context.Context, body []byte) error {
	var env struct {
		EventID     string `json:"eventId"`
		AggregateID string `json:"aggregateId"`
	}
	_ = json.Unmarshal(body, &env)
	group := env.AggregateID
	if group == "" {
		group = "default"
	}
	dedup := env.EventID
	if dedup == "" {
		sum := sha256.Sum256(body)
		dedup = hex.EncodeToString(sum[:])
	}
	_, err := c.api.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(c.eventsURL),
		MessageBody:            aws.String(string(body)),
		MessageGroupId:         aws.String(group),
		MessageDeduplicationId: aws.String(dedup),
	})
	return err
}

func (c *Client) Receive(ctx context.Context, max int32) ([]types.Message, error) {
	out, err := c.api.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(c.wagerQueueURL),
		MaxNumberOfMessages: max,
		WaitTimeSeconds:     10,
		VisibilityTimeout:   60,
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
	client *Client
	svc    *app.Service
	log    *zap.Logger
}

func NewConsumer(client *Client, svc *app.Service, log *zap.Logger) *Consumer {
	return &Consumer{client: client, svc: svc, log: log}
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
	ProviderID            string `json:"providerId"`
	ExternalTransactionID string `json:"externalTransactionId"`
	IdempotencyKey        string `json:"idempotencyKey"`
	PlayerID              string `json:"playerId"`
	WalletID              string `json:"walletId"`
	RoundID               string `json:"roundId"`
	GameID                string `json:"gameId"`
	Kind                  string `json:"kind"`
	Money                 struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"money"`
	ReferenceExternalID string `json:"referenceExternalTransactionId"`
}

func (c *Consumer) handle(ctx context.Context, m types.Message) error {
	body := aws.ToString(m.Body)
	var env inboundEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		c.log.Error("invalid envelope", zap.Error(err))
		return c.client.Delete(ctx, aws.ToString(m.ReceiptHandle))
	}
	msgID := env.MessageID
	if msgID == "" {
		msgID = aws.ToString(m.MessageId)
	}
	sum := sha256.Sum256([]byte(body))
	payloadHash := hex.EncodeToString(sum[:])

	var data inboundData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return c.client.Delete(ctx, aws.ToString(m.ReceiptHandle))
	}
	amt, err := money.Parse(data.Money.Amount, data.Money.Currency)
	if err != nil {
		return c.client.Delete(ctx, aws.ToString(m.ReceiptHandle))
	}
	kind, err := wager.ParseKind(data.Kind)
	if err != nil || kind == wager.KindOpening {
		return c.client.Delete(ctx, aws.ToString(m.ReceiptHandle))
	}
	bizHash, err := idemp.CanonicalHash(idemp.BusinessFields(
		data.ProviderID, data.ExternalTransactionID, data.PlayerID, data.WalletID,
		data.RoundID, data.GameID, data.Kind, amt.DecimalString(), amt.Currency(), data.ReferenceExternalID,
	))
	if err != nil {
		return err
	}
	_, err = c.svc.ProcessWager(ctx, app.ProcessWagerCommand{
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
		PayloadHash:           bizHash,
		CorrelationID:         msgID,
		AuthenticatedProvider: data.ProviderID,
		Inbox: &app.InboxMeta{
			ConsumerName: ConsumerName,
			MessageID:    msgID,
			PayloadHash:  payloadHash,
		},
	})
	if err != nil {
		if isPermanent(err) {
			_ = c.client.Delete(ctx, aws.ToString(m.ReceiptHandle))
			return err
		}
		return err
	}
	return c.client.Delete(ctx, aws.ToString(m.ReceiptHandle))
}

func isPermanent(err error) bool {
	return errors.Is(err, domain.ErrOpeningNotAllowed) ||
		errors.Is(err, domain.ErrInvalidKind) ||
		errors.Is(err, domain.ErrInvalidAmount) ||
		errors.Is(err, domain.ErrInvalidIdempotencyKey)
}

type Publisher struct {
	uow        app.UnitOfWork
	client     *Client
	instanceID string
	log        *zap.Logger
}

func NewPublisher(uow app.UnitOfWork, client *Client, instanceID string, log *zap.Logger) *Publisher {
	return &Publisher{uow: uow, client: client, instanceID: instanceID, log: log}
}

func (p *Publisher) Run(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := p.tick(ctx); err != nil && ctx.Err() == nil {
				p.log.Warn("outbox tick failed", zap.Error(err))
			}
		}
	}
}

func (p *Publisher) tick(ctx context.Context) error {
	tx, err := p.uow.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recs, err := tx.Outbox().Claim(ctx, p.instanceID, 30*time.Second, 20, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, rec := range recs {
		if err := p.client.Publish(ctx, rec.Payload); err != nil {
			rtx, berr := p.uow.Begin(ctx)
			if berr != nil {
				return berr
			}
			_ = rtx.Outbox().Retry(ctx, rec.ID, rec.Attempts, time.Now().UTC().Add(time.Duration(rec.Attempts)*time.Second))
			_ = rtx.Commit(ctx)
			continue
		}
		rtx, berr := p.uow.Begin(ctx)
		if berr != nil {
			return berr
		}
		if err := rtx.Outbox().MarkPublished(ctx, rec.ID, time.Now().UTC()); err != nil {
			_ = rtx.Rollback(ctx)
			return err
		}
		if err := rtx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

type ReferenceWorker struct {
	svc *app.Service
	log *zap.Logger
}

func NewReferenceWorker(svc *app.Service, log *zap.Logger) *ReferenceWorker {
	return &ReferenceWorker{svc: svc, log: log}
}

func (w *ReferenceWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.svc.RetryPendingReferences(ctx, 20); err != nil && ctx.Err() == nil {
				w.log.Warn("pending reference retry failed", zap.Error(err))
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
