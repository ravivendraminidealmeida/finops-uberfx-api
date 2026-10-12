package app

import (
	"context"
	"errors"
	"time"

	"junglegaming.io/finops-api/internal/domain"
	"junglegaming.io/finops-api/internal/domain/events"
	"junglegaming.io/finops-api/internal/domain/ledger"
	"junglegaming.io/finops-api/internal/domain/money"
	"junglegaming.io/finops-api/internal/domain/wager"
	"junglegaming.io/finops-api/internal/domain/wallet"
)

type Config struct {
	MaxReferenceAttempts int
	ReferenceBackoff     time.Duration
}

type Service struct {
	uow   UnitOfWork
	clock Clock
	ids   IDGen
	cfg   Config
}

func NewService(uow UnitOfWork, clock Clock, ids IDGen, cfg Config) *Service {
	if cfg.MaxReferenceAttempts <= 0 {
		cfg.MaxReferenceAttempts = 8
	}
	if cfg.ReferenceBackoff <= 0 {
		cfg.ReferenceBackoff = 200 * time.Millisecond
	}
	return &Service{uow: uow, clock: clock, ids: ids, cfg: cfg}
}

func (s *Service) ProcessWager(ctx context.Context, cmd ProcessWagerCommand) (ProcessResult, error) {
	if cmd.AuthenticatedProvider == "" || cmd.AuthenticatedProvider != cmd.ProviderID {
		return ProcessResult{}, domain.ErrUnauthorizedProvider
	}
	if cmd.IdempotencyKey == "" {
		return ProcessResult{}, domain.ErrInvalidIdempotencyKey
	}
	hash, err := cmd.payloadHash()
	if err != nil {
		return ProcessResult{}, err
	}

	tx, err := s.uow.Begin(ctx)
	if err != nil {
		return ProcessResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if cmd.Inbox != nil {
		inserted, existingHash, err := tx.Inbox().Insert(ctx, cmd.Inbox.ConsumerName, cmd.Inbox.MessageID, cmd.Inbox.PayloadHash, s.clock.Now())
		if err != nil {
			return ProcessResult{}, err
		}
		if !inserted && existingHash != cmd.Inbox.PayloadHash {
			return ProcessResult{}, domain.ErrPayloadConflict
		}
	}

	w, err := tx.Wallets().Lock(ctx, cmd.WalletID)
	if err != nil {
		return ProcessResult{}, err
	}
	if w.PlayerID() != cmd.PlayerID {
		return ProcessResult{}, domain.ErrReferenceMismatch
	}
	if err := w.MustMatchCurrency(cmd.Amount); err != nil {
		return ProcessResult{}, err
	}

	existing, err := tx.Wagers().GetByIdempotencyKey(ctx, cmd.ProviderID, cmd.IdempotencyKey)
	if err != nil && !domain.IsNotFound(err) {
		return ProcessResult{}, err
	}
	if existing != nil {
		if existing.PayloadHash() != hash {
			return ProcessResult{}, domain.ErrIdempotencyConflict
		}
		return s.commitReplay(ctx, tx, cmd, existing)
	}

	byExt, err := tx.Wagers().GetByExternalID(ctx, cmd.ProviderID, cmd.ExternalTransactionID)
	if err != nil && !domain.IsNotFound(err) {
		return ProcessResult{}, err
	}
	if byExt != nil {
		if byExt.IdempotencyKey() != cmd.IdempotencyKey {
			return ProcessResult{}, domain.ErrExternalIDConflict
		}
		if byExt.PayloadHash() != hash {
			return ProcessResult{}, domain.ErrIdempotencyConflict
		}
		return s.commitReplay(ctx, tx, cmd, byExt)
	}

	now := s.clock.Now()
	wt, err := wager.NewExternal(wager.NewExternalParams{
		ID:                    wager.ID(s.ids.New()),
		Kind:                  cmd.Kind,
		WalletID:              cmd.WalletID,
		PlayerID:              cmd.PlayerID,
		Amount:                cmd.Amount,
		ProviderID:            cmd.ProviderID,
		ExternalTransactionID: cmd.ExternalTransactionID,
		IdempotencyKey:        cmd.IdempotencyKey,
		PayloadHash:           hash,
		RoundID:               cmd.RoundID,
		GameID:                cmd.GameID,
		ReferenceExternalID:   cmd.ReferenceExternalID,
		Now:                   now,
	})
	if err != nil {
		return ProcessResult{}, err
	}

	if err := s.applyOperation(ctx, tx, w, wt, now); err != nil {
		return ProcessResult{}, err
	}
	if err := s.completeInbox(ctx, tx, cmd); err != nil {
		return ProcessResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProcessResult{}, err
	}
	return ProcessResult{Transaction: wt, Balance: processedBalance(wt, w.Balance())}, nil
}

func (s *Service) RetryPendingReferences(ctx context.Context, limit int) (int, error) {
	var pending []*wager.Transaction
	err := s.uow.Do(ctx, func(tx Tx) error {
		var err error
		pending, err = tx.Wagers().ListPendingReferences(ctx, s.clock.Now(), limit)
		return err
	})
	if err != nil {
		return 0, err
	}
	var first error
	for _, p := range pending {
		cmd := ProcessWagerCommand{
			ProviderID:            p.ProviderID(),
			ExternalTransactionID: p.ExternalTransactionID(),
			IdempotencyKey:        p.IdempotencyKey(),
			PlayerID:              p.PlayerID(),
			WalletID:              p.WalletID(),
			RoundID:               p.RoundID(),
			GameID:                p.GameID(),
			Kind:                  p.Kind(),
			Amount:                p.Amount(),
			ReferenceExternalID:   p.ReferenceExternalID(),
			AuthenticatedProvider: p.ProviderID(),
		}
		if _, err := s.resumePending(ctx, p, cmd); err != nil && first == nil {
			first = err
		}
	}
	return len(pending), first
}

func (s *Service) resumePending(ctx context.Context, existing *wager.Transaction, cmd ProcessWagerCommand) (ProcessResult, error) {
	tx, err := s.uow.Begin(ctx)
	if err != nil {
		return ProcessResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := tx.Wagers().LockByID(ctx, existing.ID())
	if err != nil {
		return ProcessResult{}, err
	}
	if current.Status() != wager.StatusPendingReference {
		return s.commitReplay(ctx, tx, cmd, current)
	}

	w, err := tx.Wallets().Lock(ctx, current.WalletID())
	if err != nil {
		return ProcessResult{}, err
	}
	now := s.clock.Now()
	if err := s.resolveAndApply(ctx, tx, w, current, now, false); err != nil {
		return ProcessResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProcessResult{}, err
	}
	return ProcessResult{Transaction: current, Balance: processedBalance(current, w.Balance())}, nil
}

func (s *Service) applyOperation(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, now time.Time) error {
	if wt.Kind().RequiresReference() || (wt.Kind().OptionalReference() && wt.ReferenceExternalID() != "") {
		return s.resolveAndApply(ctx, tx, w, wt, now, true)
	}
	return s.applyResolved(ctx, tx, w, wt, nil, now, true)
}

func (s *Service) resolveAndApply(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, now time.Time, insert bool) error {
	ref, err := tx.Wagers().LockByExternalID(ctx, wt.ProviderID(), wt.ReferenceExternalID())
	if domain.IsNotFound(err) {
		return s.parkReference(ctx, tx, w, wt, now, insert)
	}
	if err != nil {
		return err
	}
	return s.applyWithReference(ctx, tx, w, wt, ref, now, insert)
}

func (s *Service) parkReference(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, now time.Time, insert bool) error {
	wt.RememberBalance(w.Balance())
	attempt := wt.AttemptCount() + 1
	if attempt > s.cfg.MaxReferenceAttempts {
		return s.reject(ctx, tx, w, wt, wager.FailureReferenceNotFound, insert, now)
	}
	exp := attempt - 1
	if exp > 10 {
		exp = 10
	}
	next := now.Add(s.cfg.ReferenceBackoff * time.Duration(1<<exp))
	if err := wt.MarkPendingReference(attempt, next, now); err != nil {
		return err
	}
	if err := s.saveWager(ctx, tx, wt, insert); err != nil {
		return err
	}
	return s.appendOutbox(ctx, tx, events.PendingReference(s.ids.New(), wt, string(wt.ID()), now))
}

func (s *Service) applyWithReference(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, ref *wager.Transaction, now time.Time, insert bool) error {
	if ref.Status() != wager.StatusProcessed {
		if ref.Status() == wager.StatusPending || ref.Status() == wager.StatusPendingReference {
			return s.parkReference(ctx, tx, w, wt, now, insert)
		}
		return s.reject(ctx, tx, w, wt, wager.FailureReferenceNotProcessed, insert, now)
	}
	if ref.ProviderID() != wt.ProviderID() || ref.PlayerID() != wt.PlayerID() || ref.WalletID() != wt.WalletID() || ref.RoundID() != wt.RoundID() || ref.Amount().Currency() != wt.Amount().Currency() {
		return s.reject(ctx, tx, w, wt, wager.FailureReferenceMismatch, insert, now)
	}
	if wt.Kind() == wager.KindWin && ref.Kind() != wager.KindBet {
		return s.reject(ctx, tx, w, wt, wager.FailureReferenceMismatch, insert, now)
	}
	if wt.Kind() == wager.KindRefund && ref.Kind() != wager.KindBet {
		return s.reject(ctx, tx, w, wt, wager.FailureReferenceMismatch, insert, now)
	}
	if wt.Kind() == wager.KindRollback && ref.Kind() != wager.KindBet && ref.Kind() != wager.KindWin && ref.Kind() != wager.KindRefund {
		return s.reject(ctx, tx, w, wt, wager.FailureReferenceMismatch, insert, now)
	}
	if !wt.Kind().OptionalReference() {
		eq, err := ref.Amount().Equal(wt.Amount())
		if err != nil || !eq {
			return s.reject(ctx, tx, w, wt, wager.FailureInvalidAmount, insert, now)
		}
		dup, err := tx.Wagers().HasActiveReversal(ctx, ref.ID(), wt.ID())
		if err != nil {
			return err
		}
		if dup {
			return s.reject(ctx, tx, w, wt, wager.FailureDuplicateReversal, insert, now)
		}
	}
	wt.SetReferenced(ref.ID())
	return s.applyResolved(ctx, tx, w, wt, ref, now, insert)
}

func (s *Service) reject(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, code wager.FailureCode, insert bool, now time.Time) error {
	if w != nil {
		wt.RememberBalance(w.Balance())
	}
	if err := wt.MarkRejected(code, now); err != nil {
		return err
	}
	if err := s.saveWager(ctx, tx, wt, insert); err != nil {
		return err
	}
	return s.appendOutbox(ctx, tx, events.WagerRejected(s.ids.New(), wt, string(wt.ID()), now))
}

func (s *Service) applyResolved(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, ref *wager.Transaction, now time.Time, insert bool) error {
	if wt.Kind() == wager.KindLoss {
		if err := wt.MarkProcessed(w.Balance(), "", now); err != nil {
			return err
		}
		if err := s.saveWager(ctx, tx, wt, insert); err != nil {
			return err
		}
		return s.appendProcessed(ctx, tx, wt, nil, w.Version(), now)
	}

	refKind := wager.Kind("")
	if ref != nil {
		refKind = ref.Kind()
	}
	debit := wt.Kind().DebitsPlayer(refKind)
	if wt.Kind() == wager.KindRollback && ref == nil {
		return domain.ErrReferenceNotFound
	}

	expected := w.Version()
	var before, after money.Money
	var err error
	if debit {
		before, after, err = w.Debit(wt.Amount(), now)
		if errors.Is(err, domain.ErrInsufficientFunds) {
			code := wager.FailureInsufficientFunds
			if wt.Kind() == wager.KindRollback {
				code = wager.FailureInsufficientFundsReversal
			}
			return s.reject(ctx, tx, w, wt, code, insert, now)
		}
		if err != nil {
			return err
		}
	} else {
		before, after, err = w.Credit(wt.Amount(), now)
		if err != nil {
			return err
		}
	}
	if err := tx.Wallets().Save(ctx, w, expected); err != nil {
		return err
	}

	dir := ledger.Credit
	if debit {
		dir = ledger.Debit
	}
	entry, err := ledger.New(ledger.ID(s.ids.New()), w.ID(), string(wt.ID()), dir, wt.Amount(), before, after, now)
	if err != nil {
		return err
	}
	if err := wt.MarkProcessed(after, wt.ReferencedInternalID(), now); err != nil {
		return err
	}
	if err := s.saveWager(ctx, tx, wt, insert); err != nil {
		return err
	}
	if err := tx.Ledger().Insert(ctx, entry); err != nil {
		return err
	}
	return s.appendProcessed(ctx, tx, wt, entry, w.Version(), now)
}

func (s *Service) saveWager(ctx context.Context, tx Tx, wt *wager.Transaction, insert bool) error {
	if insert {
		return tx.Wagers().Insert(ctx, wt)
	}
	return tx.Wagers().Update(ctx, wt)
}

func (s *Service) appendProcessed(ctx context.Context, tx Tx, wt *wager.Transaction, entry *ledger.Entry, walletVersion int64, now time.Time) error {
	corr := string(wt.ID())
	if err := s.appendOutbox(ctx, tx, events.WagerProcessed(s.ids.New(), wt, corr, now)); err != nil {
		return err
	}
	if entry != nil {
		return s.appendOutbox(ctx, tx, events.BalanceChanged(s.ids.New(), entry, wt.ID(), walletVersion, corr, now))
	}
	return nil
}

func (s *Service) appendOutbox(ctx context.Context, tx Tx, env events.Envelope) error {
	return tx.Outbox().Insert(ctx, OutboxRecord{
		ID:          s.ids.New(),
		EventID:     env.EventID,
		EventType:   env.EventType,
		AggregateID: env.AggregateID,
		GroupID:     env.GroupID,
		Envelope:    env,
		OccurredAt:  env.OccurredAt,
		NextAttempt: env.OccurredAt,
	})
}

func (s *Service) completeInbox(ctx context.Context, tx Tx, cmd ProcessWagerCommand) error {
	if cmd.Inbox == nil {
		return nil
	}
	return tx.Inbox().Complete(ctx, cmd.Inbox.ConsumerName, cmd.Inbox.MessageID, s.clock.Now())
}

func (s *Service) commitReplay(ctx context.Context, tx Tx, cmd ProcessWagerCommand, existing *wager.Transaction) (ProcessResult, error) {
	res, err := s.replay(existing)
	if err != nil {
		return ProcessResult{}, err
	}
	if err := s.completeInbox(ctx, tx, cmd); err != nil {
		return ProcessResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProcessResult{}, err
	}
	return res, nil
}

func (s *Service) replay(existing *wager.Transaction) (ProcessResult, error) {
	if existing.ProviderID() == "" && existing.Origin() == wager.OriginExternal {
		return ProcessResult{}, domain.ErrUnauthorizedProvider
	}
	zero, err := money.Zero(existing.Amount().Currency())
	if err != nil {
		return ProcessResult{}, err
	}
	return ProcessResult{Transaction: existing, Balance: processedBalance(existing, zero), IdempotentReplay: true}, nil
}

func (s *Service) OpenWallet(ctx context.Context, cmd OpenWalletCommand) (OpenWalletResult, error) {
	now := s.clock.Now()
	w, err := wallet.New(wallet.ID(s.ids.New()), cmd.PlayerID, cmd.Initial, now)
	if err != nil {
		return OpenWalletResult{}, err
	}
	tx, err := s.uow.Begin(ctx)
	if err != nil {
		return OpenWalletResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.Wallets().Insert(ctx, w); err != nil {
		return OpenWalletResult{}, err
	}
	if cmd.Initial.IsPositive() {
		opening, err := wager.NewOpening(wager.ID(s.ids.New()), w.ID(), w.PlayerID(), cmd.Initial, now)
		if err != nil {
			return OpenWalletResult{}, err
		}
		zero, err := money.Zero(cmd.Initial.Currency())
		if err != nil {
			return OpenWalletResult{}, err
		}
		entry, err := ledger.New(ledger.ID(s.ids.New()), w.ID(), string(opening.ID()), ledger.Credit, cmd.Initial, zero, cmd.Initial, now)
		if err != nil {
			return OpenWalletResult{}, err
		}
		if err := tx.Wagers().Insert(ctx, opening); err != nil {
			return OpenWalletResult{}, err
		}
		if err := tx.Ledger().Insert(ctx, entry); err != nil {
			return OpenWalletResult{}, err
		}
		if err := s.appendProcessed(ctx, tx, opening, entry, w.Version(), now); err != nil {
			return OpenWalletResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return OpenWalletResult{}, err
	}
	return OpenWalletResult{Wallet: w}, nil
}

func (s *Service) GetWallet(ctx context.Context, id wallet.ID) (*wallet.Wallet, error) {
	var w *wallet.Wallet
	err := s.uow.Do(ctx, func(tx Tx) error {
		var err error
		w, err = tx.Wallets().Get(ctx, id)
		return err
	})
	return w, err
}

func (s *Service) GetTransaction(ctx context.Context, id wager.ID, provider string, internal bool) (*wager.Transaction, error) {
	var wt *wager.Transaction
	err := s.uow.Do(ctx, func(tx Tx) error {
		var err error
		wt, err = tx.Wagers().Get(ctx, id)
		if err != nil {
			return err
		}
		return authorizeRead(wt, provider, internal)
	})
	return wt, err
}

func (s *Service) GetByExternal(ctx context.Context, providerID, externalID, authProvider string) (*wager.Transaction, error) {
	if authProvider != providerID {
		return nil, domain.ErrUnauthorizedProvider
	}
	var wt *wager.Transaction
	err := s.uow.Do(ctx, func(tx Tx) error {
		var err error
		wt, err = tx.Wagers().GetByExternalID(ctx, providerID, externalID)
		return err
	})
	return wt, err
}

func (s *Service) ListLedger(ctx context.Context, walletID wallet.ID, after time.Time, afterID string, limit int) ([]*ledger.Entry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var items []*ledger.Entry
	err := s.uow.Do(ctx, func(tx Tx) error {
		var err error
		items, err = tx.Ledger().ListByWallet(ctx, walletID, after, afterID, limit)
		return err
	})
	return items, err
}

func (s *Service) Reconcile(ctx context.Context, walletID wallet.ID) (ReconcileResult, error) {
	tx, err := s.uow.BeginSnapshot(ctx)
	if err != nil {
		return ReconcileResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	w, err := tx.Wallets().Get(ctx, walletID)
	if err != nil {
		return ReconcileResult{}, err
	}
	credits, debits, count, err := tx.Ledger().SumByWallet(ctx, walletID)
	if err != nil {
		return ReconcileResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReconcileResult{}, err
	}
	calcMinor := credits - debits
	calc, err := money.FromMinor(calcMinor, w.Currency())
	if err != nil {
		return ReconcileResult{}, err
	}
	diff, err := w.Balance().Sub(calc)
	if err != nil {
		return ReconcileResult{}, err
	}
	eq, err := w.Balance().Equal(calc)
	if err != nil {
		return ReconcileResult{}, err
	}
	return ReconcileResult{
		WalletID:          walletID,
		StoredBalance:     w.Balance(),
		CalculatedBalance: calc,
		Difference:        diff,
		Consistent:        eq,
		CheckedEntries:    count,
	}, nil
}

func authorizeRead(wt *wager.Transaction, provider string, internal bool) error {
	if wt.Origin() == wager.OriginInternal {
		if !internal {
			return domain.ErrUnauthorizedInternal
		}
		return nil
	}
	if wt.ProviderID() != provider {
		return domain.ErrUnauthorizedProvider
	}
	return nil
}

func processedBalance(wt *wager.Transaction, fallback money.Money) money.Money {
	if bal, ok := wt.ResultBalance(); ok {
		return bal
	}
	return fallback
}
