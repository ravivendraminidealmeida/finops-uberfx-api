//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"
	tpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"junglegaming.io/finops-api/internal/adapter/postgres"
	sqsadapter "junglegaming.io/finops-api/internal/adapter/sqs"
	"junglegaming.io/finops-api/internal/app"
	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
	"junglegaming.io/finops-api/internal/platform/config"
	"junglegaming.io/finops-api/internal/platform/fxapp"
)

func TestMain(m *testing.M) {
	if os.Getenv("DOCKER_HOST") == "" {
		for _, sock := range []string{
			os.Getenv("HOME") + "/.colima/default/docker.sock",
			os.Getenv("HOME") + "/.docker/run/docker.sock",
		} {
			if _, err := os.Stat(sock); err == nil {
				_ = os.Setenv("DOCKER_HOST", "unix://"+sock)
				break
			}
		}
	}
	if os.Getenv("TESTCONTAINERS_RYUK_DISABLED") == "" {
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}
	if os.Getenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE") == "" {
		_ = os.Setenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE", "/var/run/docker.sock")
	}
	os.Exit(m.Run())
}

func startPostgres(t *testing.T) (string, *pgxpool.Pool, app.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	ctr, err := tpostgres.Run(ctx, "postgres:16-alpine",
		tpostgres.WithDatabase("finops"),
		tpostgres.WithUsername("finops"),
		tpostgres.WithPassword("finops"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Skipf("docker required for integration tests: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return url, pool, postgres.NewUoW(pool)
}

func newSvc(uow app.UnitOfWork) *app.Service {
	return app.NewService(uow, app.SystemClock{}, app.UUIDGen{}, app.Config{
		MaxReferenceAttempts: 5,
		ReferenceBackoff:     50 * time.Millisecond,
	})
}

func openWallet(t *testing.T, svc *app.Service, amount string) *wallet.Wallet {
	t.Helper()
	amt, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.OpenWallet(context.Background(), app.OpenWalletCommand{
		PlayerID:        wallet.PlayerID(uuid.NewString()),
		Initial:         amt,
		InternalService: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.Wallet
}

func betCmd(w *wallet.Wallet, ext, key, amount, provider string) app.ProcessWagerCommand {
	amt, _ := money.Parse(amount, "BRL")
	return app.ProcessWagerCommand{
		ProviderID:            provider,
		ExternalTransactionID: ext,
		IdempotencyKey:        key,
		PlayerID:              w.PlayerID(),
		WalletID:              w.ID(),
		RoundID:               "round-1",
		GameID:                "fortune-chimp",
		Kind:                  wager.KindBet,
		Amount:                amt,
		PayloadHash:           key + ":" + amount,
		AuthenticatedProvider: provider,
	}
}

func TestConcurrentTwoBetsEightyOnOneHundred(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	var wg sync.WaitGroup
	results := make([]app.ProcessResult, 2)
	errs := make([]error, 2)
	cmds := []app.ProcessWagerCommand{
		betCmd(w, "tx-1", "provider-a:tx-1", "80.00", "provider-a"),
		betCmd(w, "tx-2", "provider-a:tx-2", "80.00", "provider-a"),
	}
	for i := range cmds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.ProcessWager(context.Background(), cmds[i])
		}(i)
	}
	wg.Wait()
	processed, rejected := 0, 0
	for i, r := range results {
		if errs[i] != nil {
			t.Fatalf("err %v", errs[i])
		}
		switch r.Transaction.Status() {
		case wager.StatusProcessed:
			processed++
		case wager.StatusRejected:
			rejected++
			if r.Transaction.FailureCode() != wager.FailureInsufficientFunds {
				t.Fatalf("code %s", r.Transaction.FailureCode())
			}
		}
	}
	if processed != 1 || rejected != 1 {
		t.Fatalf("processed=%d rejected=%d", processed, rejected)
	}
	got, err := svc.GetWallet(context.Background(), w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance().DecimalString() != "20.00" {
		t.Fatalf("balance %s", got.Balance().DecimalString())
	}
	rec, err := svc.Reconcile(context.Background(), w.ID())
	if err != nil || !rec.Consistent || rec.CheckedEntries != 2 {
		t.Fatalf("reconcile %+v err=%v", rec, err)
	}
}

func TestFiftyDuplicateBets(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	cmd := betCmd(w, "tx-dup", "provider-a:tx-dup", "10.00", "provider-a")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.ProcessWager(context.Background(), cmd)
		}()
	}
	wg.Wait()
	got, err := svc.GetWallet(context.Background(), w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance().DecimalString() != "90.00" {
		t.Fatalf("balance %s", got.Balance().DecimalString())
	}
	rec, _ := svc.Reconcile(context.Background(), w.ID())
	if !rec.Consistent {
		t.Fatalf("inconsistent %+v", rec)
	}
}

func TestIndependentWalletsParallel(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w1 := openWallet(t, svc, "50.00")
	w2 := openWallet(t, svc, "50.00")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.ProcessWager(context.Background(), betCmd(w1, "a", "provider-a:a", "10.00", "provider-a"))
		if err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		_, err := svc.ProcessWager(context.Background(), betCmd(w2, "b", "provider-a:b", "10.00", "provider-a"))
		if err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	g1, _ := svc.GetWallet(context.Background(), w1.ID())
	g2, _ := svc.GetWallet(context.Background(), w2.ID())
	if g1.Balance().DecimalString() != "40.00" || g2.Balance().DecimalString() != "40.00" {
		t.Fatalf("%s %s", g1.Balance().DecimalString(), g2.Balance().DecimalString())
	}
}

func TestPendingReferenceThenResolve(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	bet := betCmd(w, "bet-1", "provider-a:bet-1", "10.00", "provider-a")
	amt, _ := money.Parse("10.00", "BRL")
	refund := app.ProcessWagerCommand{
		ProviderID:            "provider-a",
		ExternalTransactionID: "ref-1",
		IdempotencyKey:        "provider-a:ref-1",
		PlayerID:              w.PlayerID(),
		WalletID:              w.ID(),
		RoundID:               "round-1",
		GameID:                "fortune-chimp",
		Kind:                  wager.KindRefund,
		Amount:                amt,
		ReferenceExternalID:   "bet-1",
		PayloadHash:           "refund-hash",
		AuthenticatedProvider: "provider-a",
	}
	res, err := svc.ProcessWager(context.Background(), refund)
	if err != nil {
		t.Fatal(err)
	}
	if res.Transaction.Status() != wager.StatusPendingReference {
		t.Fatalf("status %s", res.Transaction.Status())
	}
	if _, err := svc.ProcessWager(context.Background(), bet); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = svc.RetryPendingReferences(context.Background(), 10)
		tx, err := svc.GetByExternal(context.Background(), "provider-a", "ref-1", "provider-a")
		if err == nil && tx.Status() == wager.StatusProcessed {
			got, _ := svc.GetWallet(context.Background(), w.ID())
			if got.Balance().DecimalString() != "100.00" {
				t.Fatalf("balance %s", got.Balance().DecimalString())
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("refund was not resolved")
}

func TestProviderIsolation(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	cmd := betCmd(w, "tx-iso", "provider-a:tx-iso", "5.00", "provider-a")
	if _, err := svc.ProcessWager(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	_, err := svc.GetByExternal(context.Background(), "provider-a", "tx-iso", "provider-b")
	if !errors.Is(err, domain.ErrUnauthorizedProvider) {
		t.Fatalf("got %v", err)
	}
}

func TestIdempotencyConflict(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	cmd := betCmd(w, "tx-c", "same-key", "5.00", "provider-a")
	if _, err := svc.ProcessWager(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	cmd.PayloadHash = "other"
	cmd.ExternalTransactionID = "tx-c2"
	_, err := svc.ProcessWager(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected conflict")
	}
}

func TestSQSInboxAndOutbox(t *testing.T) {
	pgURL, _, uow := startPostgres(t)
	ctx := context.Background()
	ls, err := localstack.Run(ctx, "localstack/localstack:3.8", testcontainers.WithWaitStrategy(
		wait.ForHTTP("/_localstack/health").WithPort("4566/tcp"),
	))
	if err != nil {
		t.Skipf("docker required for integration tests: %v", err)
	}
	t.Cleanup(func() { _ = ls.Terminate(context.Background()) })
	endpoint, err := ls.PortEndpoint(ctx, "4566/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		DatabaseURL:     pgURL,
		AWSRegion:       "us-east-1",
		AWSEndpoint:     endpoint,
		AWSAccessKey:    "test",
		AWSSecretKey:    "test",
		WagerQueueName:  "wager-transactions.fifo",
		DLQName:         "wager-transactions-dlq.fifo",
		EventsQueueName: "domain-events.fifo",
		InstanceID:      "test-1",
	}
	client, err := sqsadapter.NewClient(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	body, _ := json.Marshal(map[string]any{
		"messageId":  "msg-1",
		"type":       "WagerTransactionRequested",
		"occurredAt": time.Now().UTC().Format(time.RFC3339),
		"data": map[string]any{
			"providerId":            "provider-a",
			"externalTransactionId": "sqs-1",
			"idempotencyKey":        "provider-a:sqs-1",
			"playerId":              string(w.PlayerID()),
			"walletId":              string(w.ID()),
			"roundId":               "round-1",
			"gameId":                "fortune-chimp",
			"kind":                  "BET",
			"money":                 map[string]string{"amount": "7.00", "currency": "BRL"},
		},
	})
	if err := client.SendWager(ctx, body, string(w.ID()), "msg-1"); err != nil {
		t.Fatal(err)
	}
	consumer := sqsadapter.NewConsumer(client, svc, zap.NewNop())
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { _ = consumer.Run(cctx); close(done) }()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.GetWallet(ctx, w.ID())
		if err == nil && got.Balance().DecimalString() == "93.00" {
			cancel()
			<-done
			if err := client.SendWager(ctx, body, string(w.ID()), "msg-1-redeliver"); err != nil {
				t.Fatal(err)
			}
			got2, _ := svc.GetWallet(ctx, w.ID())
			_ = got2
			pub := sqsadapter.NewPublisher(uow, client, "pub-1", zap.NewNop())
			pctx, pcancel := context.WithTimeout(ctx, 3*time.Second)
			go func() { _ = pub.Run(pctx) }()
			time.Sleep(500 * time.Millisecond)
			pcancel()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("sqs bet not processed")
}

func TestFxLifecycle(t *testing.T) {
	pgURL, _, _ := startPostgres(t)
	ctx := context.Background()
	ls, err := localstack.Run(ctx, "localstack/localstack:3.8")
	if err != nil {
		t.Skip("localstack:", err)
	}
	t.Cleanup(func() { _ = ls.Terminate(context.Background()) })
	endpoint, _ := ls.PortEndpoint(ctx, "4566/tcp", "http")
	iss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := issURL(r)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, ".well-known") {
			fmt.Fprintf(w, `{"issuer":%q,"jwks_uri":%q,"authorization_endpoint":%q,"token_endpoint":%q,"id_token_signing_alg_values_supported":["RS256"]}`,
				base, base+"/jwks", base+"/auth", base+"/token")
			return
		}
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	t.Cleanup(iss.Close)
	t.Setenv("DATABASE_URL", pgURL)
	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("OIDC_ISSUER", iss.URL)
	t.Setenv("HTTP_ADDR", "127.0.0.1:0")
	app := fx.New(fxapp.Module(), fx.NopLogger)
	startCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
}

func issURL(r *http.Request) string {
	return "http://" + r.Host
}
