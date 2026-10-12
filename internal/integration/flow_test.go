//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
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
	"junglegaming.io/finops-api/internal/platform/obs"
)

var (
	pgOnce sync.Once
	pgURL  string
	pgErr  error
	kcOnce sync.Once
	kcURL  string
	kcErr  error
)

func postgresURL(t *testing.T) string {
	t.Helper()
	pgOnce.Do(func() {
		ctx := context.Background()
		ctr, err := tpostgres.Run(ctx, "postgres:16-alpine",
			tpostgres.WithDatabase("finops"),
			tpostgres.WithUsername("finops"),
			tpostgres.WithPassword("finops"),
			testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
		)
		if err != nil {
			pgErr = fmt.Errorf("postgres container: %w", err)
			return
		}
		pgURL, err = ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			pgErr = err
			return
		}
		pgErr = postgres.RunMigrations(pgURL)
	})
	if pgErr != nil {
		t.Fatalf("%v", pgErr)
	}
	return pgURL
}

func startPostgres(t *testing.T) (string, *pgxpool.Pool, app.UnitOfWork) {
	t.Helper()
	u := postgresURL(t)
	pool, err := pgxpool.New(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return u, pool, postgres.NewUoW(pool)
}

func keycloakIssuer(t *testing.T) string {
	t.Helper()
	kcOnce.Do(func() {
		ctx := context.Background()
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			kcErr = fmt.Errorf("runtime.Caller")
			return
		}
		realm := filepath.Join(filepath.Dir(file), "../../deploy/keycloak/finops-realm.json")
		ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: "keycloak/keycloak:26.0",
				Cmd:   []string{"start-dev", "--import-realm", "--hostname-strict=false"},
				Env: map[string]string{
					"KEYCLOAK_ADMIN":          "admin",
					"KEYCLOAK_ADMIN_PASSWORD": "admin",
				},
				Files: []testcontainers.ContainerFile{{
					HostFilePath:      realm,
					ContainerFilePath: "/opt/keycloak/data/import/finops-realm.json",
					FileMode:          0o644,
				}},
				ExposedPorts: []string{"8080/tcp"},
				WaitingFor: wait.ForHTTP("/realms/finops/.well-known/openid-configuration").
					WithPort("8080/tcp").
					WithStartupTimeout(2 * time.Minute),
			},
			Started: true,
		})
		if err != nil {
			kcErr = fmt.Errorf("keycloak container: %w", err)
			return
		}
		host, err := ctr.Host(ctx)
		if err != nil {
			kcErr = err
			return
		}
		port, err := ctr.MappedPort(ctx, "8080/tcp")
		if err != nil {
			kcErr = err
			return
		}
		kcURL = fmt.Sprintf("http://%s:%s/realms/finops", host, port.Port())
	})
	if kcErr != nil {
		t.Fatalf("%v", kcErr)
	}
	return kcURL
}

func clientToken(t *testing.T, issuer, clientID, secret string) string {
	t.Helper()
	resp, err := http.PostForm(issuer+"/protocol/openid-connect/token", url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token %d %s", resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.AccessToken == "" {
		t.Fatalf("token body %s", raw)
	}
	return out.AccessToken
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
		PlayerID: wallet.PlayerID(uuid.NewString()),
		Initial:  amt,
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
	results := make([]app.ProcessResult, 50)
	errs := make([]error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.ProcessWager(context.Background(), cmd)
		}(i)
	}
	wg.Wait()
	fresh, replay := 0, 0
	for i, r := range results {
		if errs[i] != nil {
			t.Fatalf("err %v", errs[i])
		}
		if r.Balance.DecimalString() != "90.00" {
			t.Fatalf("replay balance %s", r.Balance.DecimalString())
		}
		if r.IdempotentReplay {
			replay++
		} else {
			fresh++
		}
	}
	if fresh != 1 || replay != 49 {
		t.Fatalf("fresh=%d replay=%d", fresh, replay)
	}
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
		_, _ = svc.RetryPendingReferences(context.Background(), 10)
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
	cmd.ExternalTransactionID = "tx-c2"
	_, err := svc.ProcessWager(context.Background(), cmd)
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("got %v", err)
	}
}

func TestRefundAndRollbackSameBetAreExclusive(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	if _, err := svc.ProcessWager(context.Background(), betCmd(w, "bet-x", "provider-a:bet-x", "10.00", "provider-a")); err != nil {
		t.Fatal(err)
	}
	amt, _ := money.Parse("10.00", "BRL")
	refund := app.ProcessWagerCommand{
		ProviderID: "provider-a", ExternalTransactionID: "ref-x", IdempotencyKey: "provider-a:ref-x",
		PlayerID: w.PlayerID(), WalletID: w.ID(), RoundID: "round-1", GameID: "fortune-chimp",
		Kind: wager.KindRefund, Amount: amt, ReferenceExternalID: "bet-x",
		AuthenticatedProvider: "provider-a",
	}
	if _, err := svc.ProcessWager(context.Background(), refund); err != nil {
		t.Fatal(err)
	}
	rollback := refund
	rollback.ExternalTransactionID = "rb-x"
	rollback.IdempotencyKey = "provider-a:rb-x"
	rollback.Kind = wager.KindRollback
	res, err := svc.ProcessWager(context.Background(), rollback)
	if err != nil {
		t.Fatal(err)
	}
	if res.Transaction.Status() != wager.StatusRejected || res.Transaction.FailureCode() != wager.FailureDuplicateReversal {
		t.Fatalf("status %s code %s", res.Transaction.Status(), res.Transaction.FailureCode())
	}
	got, err := svc.GetWallet(context.Background(), w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance().DecimalString() != "100.00" {
		t.Fatalf("double credit: %s", got.Balance().DecimalString())
	}
}

func TestRollbackOfRefund(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	if _, err := svc.ProcessWager(context.Background(), betCmd(w, "bet-rr", "provider-a:bet-rr", "10.00", "provider-a")); err != nil {
		t.Fatal(err)
	}
	amt, _ := money.Parse("10.00", "BRL")
	refund := app.ProcessWagerCommand{
		ProviderID: "provider-a", ExternalTransactionID: "refund-rr", IdempotencyKey: "provider-a:refund-rr",
		PlayerID: w.PlayerID(), WalletID: w.ID(), RoundID: "round-1", GameID: "fortune-chimp",
		Kind: wager.KindRefund, Amount: amt, ReferenceExternalID: "bet-rr",
		AuthenticatedProvider: "provider-a",
	}
	if _, err := svc.ProcessWager(context.Background(), refund); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetWallet(context.Background(), w.ID())
	if err != nil || got.Balance().DecimalString() != "100.00" {
		t.Fatalf("after refund %v %v", err, got)
	}
	rollback := refund
	rollback.ExternalTransactionID = "rb-refund"
	rollback.IdempotencyKey = "provider-a:rb-refund"
	rollback.Kind = wager.KindRollback
	rollback.ReferenceExternalID = "refund-rr"
	res, err := svc.ProcessWager(context.Background(), rollback)
	if err != nil {
		t.Fatal(err)
	}
	if res.Transaction.Status() != wager.StatusProcessed {
		t.Fatalf("status %s code %s", res.Transaction.Status(), res.Transaction.FailureCode())
	}
	got, err = svc.GetWallet(context.Background(), w.ID())
	if err != nil || got.Balance().DecimalString() != "90.00" {
		t.Fatalf("after rollback of refund: %v %s", err, got.Balance().DecimalString())
	}
}

func TestWinCreditsWithoutReference(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	if _, err := svc.ProcessWager(context.Background(), betCmd(w, "bet-w", "provider-a:bet-w", "10.00", "provider-a")); err != nil {
		t.Fatal(err)
	}
	amt, _ := money.Parse("25.00", "BRL")
	win := app.ProcessWagerCommand{
		ProviderID: "provider-a", ExternalTransactionID: "win-w", IdempotencyKey: "provider-a:win-w",
		PlayerID: w.PlayerID(), WalletID: w.ID(), RoundID: "round-1", GameID: "fortune-chimp",
		Kind: wager.KindWin, Amount: amt, AuthenticatedProvider: "provider-a",
	}
	res, err := svc.ProcessWager(context.Background(), win)
	if err != nil {
		t.Fatal(err)
	}
	if res.Transaction.Status() != wager.StatusProcessed {
		t.Fatalf("status %s", res.Transaction.Status())
	}
	got, err := svc.GetWallet(context.Background(), w.ID())
	if err != nil || got.Balance().DecimalString() != "115.00" {
		t.Fatalf("got %v %v", err, got)
	}
}

func TestWinOptionalBetReference(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	amt, _ := money.Parse("25.00", "BRL")
	win := app.ProcessWagerCommand{
		ProviderID: "provider-a", ExternalTransactionID: "win-ref", IdempotencyKey: "provider-a:win-ref",
		PlayerID: w.PlayerID(), WalletID: w.ID(), RoundID: "round-1", GameID: "fortune-chimp",
		Kind: wager.KindWin, Amount: amt, ReferenceExternalID: "bet-win",
		AuthenticatedProvider: "provider-a",
	}
	res, err := svc.ProcessWager(context.Background(), win)
	if err != nil {
		t.Fatal(err)
	}
	if res.Transaction.Status() != wager.StatusPendingReference {
		t.Fatalf("status %s", res.Transaction.Status())
	}
	if _, err := svc.ProcessWager(context.Background(), betCmd(w, "bet-win", "provider-a:bet-win", "10.00", "provider-a")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _ = svc.RetryPendingReferences(context.Background(), 10)
		tx, err := svc.GetByExternal(context.Background(), "provider-a", "win-ref", "provider-a")
		if err == nil && tx.Status() == wager.StatusProcessed {
			got, _ := svc.GetWallet(context.Background(), w.ID())
			if got.Balance().DecimalString() != "115.00" {
				t.Fatalf("balance %s", got.Balance().DecimalString())
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("win was not resolved")
}

func TestCurrencyMismatchRejected(t *testing.T) {
	_, _, uow := startPostgres(t)
	svc := newSvc(uow)
	w := openWallet(t, svc, "100.00")
	cmd := betCmd(w, "tx-usd", "provider-a:tx-usd", "5.00", "provider-a")
	usd, _ := money.Parse("5.00", "USD")
	cmd.Amount = usd
	_, err := svc.ProcessWager(context.Background(), cmd)
	if !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestThreeServiceInstances(t *testing.T) {
	url, _, uow1 := startPostgres(t)
	ctx := context.Background()
	pool2, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool2.Close() })
	pool3, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool3.Close() })
	svcs := []*app.Service{newSvc(uow1), newSvc(postgres.NewUoW(pool2)), newSvc(postgres.NewUoW(pool3))}
	w := openWallet(t, svcs[0], "100.00")
	cmd := betCmd(w, "tx-3", "provider-a:tx-3", "10.00", "provider-a")
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := svcs[i].ProcessWager(ctx, cmd); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := svcs[0].GetWallet(ctx, w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance().DecimalString() != "90.00" {
		t.Fatalf("balance %s", got.Balance().DecimalString())
	}
}

func startLocalstack(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ls, err := localstack.Run(ctx, "localstack/localstack:3.8", testcontainers.WithWaitStrategy(
		wait.ForHTTP("/_localstack/health").WithPort("4566/tcp"),
	))
	if err != nil {
		t.Fatalf("localstack: %v", err)
	}
	t.Cleanup(func() { _ = ls.Terminate(context.Background()) })
	endpoint, err := ls.PortEndpoint(ctx, "4566/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func TestSQSInboxAndOutbox(t *testing.T) {
	pgURL, _, uow := startPostgres(t)
	ctx := context.Background()
	cfg := config.Config{
		DatabaseURL:     pgURL,
		AWSRegion:       "us-east-1",
		AWSEndpoint:     startLocalstack(t),
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
	metrics := obs.NewMetrics()
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
	consumer := sqsadapter.NewConsumer(client, svc, zap.NewNop(), metrics)
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
			cctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
			go func() { _ = consumer.Run(cctx2) }()
			time.Sleep(800 * time.Millisecond)
			cancel2()
			got2, err := svc.GetWallet(ctx, w.ID())
			if err != nil || got2.Balance().DecimalString() != "93.00" {
				t.Fatalf("redelivery mutated balance: %v %v", err, got2)
			}
			pub := sqsadapter.NewPublisher(uow, client, "pub-1", app.SystemClock{}, zap.NewNop(), metrics)
			pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
			go func() { _ = pub.Run(pctx) }()
			evDeadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(evDeadline) {
				msgs, err := client.ReceiveEvents(ctx)
				if err == nil && len(msgs) > 0 {
					pcancel()
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
			pcancel()
			t.Fatal("outbox event was not published")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("sqs bet not processed")
}

func TestInvalidSQSMessageGoesToDLQ(t *testing.T) {
	pgURL, _, uow := startPostgres(t)
	ctx := context.Background()
	cfg := config.Config{
		DatabaseURL:     pgURL,
		AWSRegion:       "us-east-1",
		AWSEndpoint:     startLocalstack(t),
		AWSAccessKey:    "test",
		AWSSecretKey:    "test",
		WagerQueueName:  "wager-poison.fifo",
		DLQName:         "wager-poison-dlq.fifo",
		EventsQueueName: "domain-events-poison.fifo",
		InstanceID:      "test-dlq",
	}
	client, err := sqsadapter.NewClient(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	client.SetReceiveOptions(0, 1)
	if err := client.SendWager(ctx, []byte(`{not-json`), "g1", "poison-1"); err != nil {
		t.Fatal(err)
	}
	consumer := sqsadapter.NewConsumer(client, newSvc(uow), zap.NewNop(), obs.NewMetrics())
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	go func() { _ = consumer.Run(cctx) }()
	deadline := time.Now().Add(18 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := client.ReceiveDLQ(ctx)
		if err == nil && len(msgs) > 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("poison message did not reach the DLQ")
}

func TestFxLifecycle(t *testing.T) {
	pgURL, _, _ := startPostgres(t)
	ctx := context.Background()
	endpoint := startLocalstack(t)
	t.Setenv("DATABASE_URL", pgURL)
	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("OIDC_ISSUER", keycloakIssuer(t))
	t.Setenv("HTTP_ADDR", "127.0.0.1:0")
	app := fx.New(fxapp.Module(), fx.NopLogger)
	startCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
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
