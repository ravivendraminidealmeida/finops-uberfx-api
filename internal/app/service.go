package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		cfg.ReferenceBackoff = time.Second
	}
	if clock == nil {
		clock = SystemClock{}
	}
	if ids == nil {
		ids = UUIDGen{}
	}
	return &Service{uow: uow, clock: clock, ids: ids, cfg: cfg}
}

func (s *Service) ProcessWager(ctx context.Context, cmd ProcessWagerCommand) (ProcessResult, error) {
	if cmd.AuthenticatedProvider == "" {
		return ProcessResult{}, domain.ErrUnauthorizedProvider
	}
	if cmd.AuthenticatedProvider != cmd.ProviderID {
		return ProcessResult{}, domain.ErrUnauthorizedProvider
	}
	if cmd.IdempotencyKey == "" {
		return ProcessResult{}, domain.ErrInvalidIdempotencyKey
	}
	if cmd.Kind == wager.KindOpening {
		return ProcessResult{}, domain.ErrOpeningNotAllowed
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
		if !inserted {
			if existingHash != cmd.Inbox.PayloadHash {
				return ProcessResult{}, domain.ErrPayloadConflict
			}
		}
	}

	existing, err := tx.Wagers().GetByIdempotencyKey(ctx, cmd.IdempotencyKey)
	if err != nil && !errors.Is(err, domain.ErrWalletNotFound) && !isNotFound(err) {
		return ProcessResult{}, err
	}
	if existing != nil {
		if existing.PayloadHash() != cmd.PayloadHash {
			return ProcessResult{}, domain.ErrIdempotencyConflict
		}
		if existing.ProviderID() != cmd.AuthenticatedProvider {
			return ProcessResult{}, domain.ErrUnauthorizedProvider
		}
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

	byExt, err := tx.Wagers().GetByExternalID(ctx, cmd.ProviderID, cmd.ExternalTransactionID)
	if err != nil && !isNotFound(err) {
		return ProcessResult{}, err
	}
	if byExt != nil {
		if byExt.IdempotencyKey() != cmd.IdempotencyKey {
			return ProcessResult{}, domain.ErrExternalIDConflict
		}
		if byExt.PayloadHash() != cmd.PayloadHash {
			return ProcessResult{}, domain.ErrIdempotencyConflict
		}
		res, err := s.replay(byExt)
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

	w, err := tx.Wallets().Get(ctx, cmd.WalletID)
	if err != nil {
		return ProcessResult{}, err
	}
	if w.PlayerID() != cmd.PlayerID {
		return ProcessResult{}, domain.ErrReferenceMismatch
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
		PayloadHash:           cmd.PayloadHash,
		RoundID:               cmd.RoundID,
		GameID:                cmd.GameID,
		ReferenceExternalID:   cmd.ReferenceExternalID,
		Now:                   now,
	})
	if err != nil {
		return ProcessResult{}, err
	}

	if err := s.applyOperation(ctx, tx, w, wt, now); err != nil {
		if errors.Is(err, domain.ErrIdempotencyConflict) || errors.Is(err, domain.ErrExternalIDConflict) {
			_ = tx.Rollback(ctx)
			return s.lookupReplay(ctx, cmd)
		}
		if errors.Is(err, domain.ErrDuplicateReversal) {
			_ = tx.Rollback(ctx)
			return s.rejectDuplicate(ctx, cmd)
		}
		return ProcessResult{}, err
	}

	if err := s.completeInbox(ctx, tx, cmd); err != nil {
		return ProcessResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProcessResult{}, err
	}

	bal, _ := money.FromMinor(deref(wt.ResultBalanceMinor(), w.Balance().Minor()), w.Currency())
	if wt.Status() == wager.StatusProcessed && wt.ResultBalanceMinor() != nil {
		bal, _ = money.FromMinor(*wt.ResultBalanceMinor(), w.Currency())
	} else {
		fresh, err := s.GetWallet(ctx, cmd.WalletID)
		if err == nil {
			bal = fresh.Balance()
		}
	}
	return ProcessResult{Transaction: wt, Balance: bal}, nil
}

func (s *Service) RetryPendingReferences(ctx context.Context, limit int) error {
	tx, err := s.uow.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	pending, err := tx.Wagers().ListPendingReferences(ctx, s.clock.Now(), limit)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
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
			PayloadHash:           p.PayloadHash(),
			CorrelationID:         string(p.ID()),
			AuthenticatedProvider: p.ProviderID(),
		}
		if _, err := s.resumePending(ctx, p, cmd); err != nil {
			return err
		}
	}
	return nil
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
		res, err := s.replay(current)
		if err != nil {
			return ProcessResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ProcessResult{}, err
		}
		return res, nil
	}

	w, err := tx.Wallets().Get(ctx, current.WalletID())
	if err != nil {
		return ProcessResult{}, err
	}
	now := s.clock.Now()
	if err := s.resolveAndApply(ctx, tx, w, current, now); err != nil {
		return ProcessResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProcessResult{}, err
	}
	bal := w.Balance()
	if current.ResultBalanceMinor() != nil {
		bal, _ = money.FromMinor(*current.ResultBalanceMinor(), w.Currency())
	}
	return ProcessResult{Transaction: current, Balance: bal}, nil
}

func (s *Service) applyOperation(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, now time.Time) error {
	if wt.Kind() == wager.KindRefund || wt.Kind() == wager.KindRollback {
		return s.resolveAndApplyNew(ctx, tx, w, wt, now)
	}
	return s.applyResolved(ctx, tx, w, wt, nil, now, true)
}

func (s *Service) resolveAndApplyNew(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, now time.Time) error {
	ref, err := tx.Wagers().LockByExternalID(ctx, wt.ProviderID(), wt.ReferenceExternalID())
	if isNotFound(err) {
		return s.parkReference(ctx, tx, w, wt, now, true)
	}
	if err != nil {
		return err
	}
	return s.applyWithReference(ctx, tx, w, wt, ref, now, true)
}

func (s *Service) resolveAndApply(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, now time.Time) error {
	ref, err := tx.Wagers().LockByExternalID(ctx, wt.ProviderID(), wt.ReferenceExternalID())
	if isNotFound(err) {
		return s.parkReference(ctx, tx, w, wt, now, false)
	}
	if err != nil {
		return err
	}
	return s.applyWithReference(ctx, tx, w, wt, ref, now, false)
}

func (s *Service) parkReference(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, now time.Time, insert bool) error {
	_ = w
	attempt := wt.AttemptCount() + 1
	if attempt > s.cfg.MaxReferenceAttempts {
		if err := wt.MarkRejected(wager.FailureReferenceNotFound, now); err != nil {
			return err
		}
		if err := s.saveWager(ctx, tx, wt, insert); err != nil {
			return err
		}
		return s.appendRejected(ctx, tx, wt, cmdCorrelation(wt), now)
	}
	next := now.Add(s.cfg.ReferenceBackoff * time.Duration(1<<min(attempt-1, 10)))
	if err := wt.MarkPendingReference(attempt, next, now); err != nil {
		return err
	}
	if err := s.saveWager(ctx, tx, wt, insert); err != nil {
		return err
	}
	env := events.PendingReference(s.ids.New(), wt, cmdCorrelation(wt), now)
	return s.appendOutbox(ctx, tx, env)
}

func (s *Service) applyWithReference(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, ref *wager.Transaction, now time.Time, insert bool) error {
	if ref.Status() != wager.StatusProcessed {
		if ref.Status() == wager.StatusPending || ref.Status() == wager.StatusPendingReference {
			return s.parkReference(ctx, tx, w, wt, now, insert)
		}
		if err := wt.MarkRejected(wager.FailureReferenceNotProcessed, now); err != nil {
			return err
		}
		if err := s.saveWager(ctx, tx, wt, insert); err != nil {
			return err
		}
		return s.appendRejected(ctx, tx, wt, cmdCorrelation(wt), now)
	}
	if ref.ProviderID() != wt.ProviderID() || ref.PlayerID() != wt.PlayerID() || ref.WalletID() != wt.WalletID() || ref.RoundID() != wt.RoundID() || ref.Amount().Currency() != wt.Amount().Currency() {
		if err := wt.MarkRejected(wager.FailureReferenceMismatch, now); err != nil {
			return err
		}
		if err := s.saveWager(ctx, tx, wt, insert); err != nil {
			return err
		}
		return s.appendRejected(ctx, tx, wt, cmdCorrelation(wt), now)
	}
	cmp, err := ref.Amount().Cmp(wt.Amount())
	if err != nil || cmp != 0 {
		if err := wt.MarkRejected(wager.FailureInvalidAmount, now); err != nil {
			return err
		}
		if err := s.saveWager(ctx, tx, wt, insert); err != nil {
			return err
		}
		return s.appendRejected(ctx, tx, wt, cmdCorrelation(wt), now)
	}
	if wt.Kind() == wager.KindRefund && ref.Kind() != wager.KindBet {
		if err := wt.MarkRejected(wager.FailureReferenceMismatch, now); err != nil {
			return err
		}
		if err := s.saveWager(ctx, tx, wt, insert); err != nil {
			return err
		}
		return s.appendRejected(ctx, tx, wt, cmdCorrelation(wt), now)
	}
	if wt.Kind() == wager.KindRollback && ref.Kind() != wager.KindBet && ref.Kind() != wager.KindWin && ref.Kind() != wager.KindRefund {
		if err := wt.MarkRejected(wager.FailureReferenceMismatch, now); err != nil {
			return err
		}
		if err := s.saveWager(ctx, tx, wt, insert); err != nil {
			return err
		}
		return s.appendRejected(ctx, tx, wt, cmdCorrelation(wt), now)
	}
	dup, err := tx.Wagers().HasActiveReversal(ctx, ref.ID(), wt.Kind(), wt.ID())
	if err != nil {
		return err
	}
	if dup {
		if err := wt.MarkRejected(wager.FailureDuplicateReversal, now); err != nil {
			return err
		}
		if err := s.saveWager(ctx, tx, wt, insert); err != nil {
			return err
		}
		return s.appendRejected(ctx, tx, wt, cmdCorrelation(wt), now)
	}
	wt.SetReferenced(ref.ID())
	return s.applyResolved(ctx, tx, w, wt, ref, now, insert)
}

func (s *Service) applyResolved(ctx context.Context, tx Tx, w *wallet.Wallet, wt *wager.Transaction, ref *wager.Transaction, now time.Time, insert bool) error {
	corr := cmdCorrelation(wt)

	if wt.Kind() == wager.KindLoss {
		if err := wt.MarkProcessed(w.Balance(), "", now); err != nil {
			return err
		}
		if err := s.saveWager(ctx, tx, wt, insert); err != nil {
			return err
		}
		return s.appendProcessed(ctx, tx, wt, nil, w.Version(), corr, now)
	}

	debit := false
	switch wt.Kind() {
	case wager.KindBet:
		debit = true
	case wager.KindWin, wager.KindRefund:
		debit = false
	case wager.KindRollback:
		if ref == nil {
			return domain.ErrReferenceNotFound
		}
		switch ref.Kind() {
		case wager.KindBet:
			debit = false
		case wager.KindWin, wager.KindRefund:
			debit = true
		}
	}

	var before, after money.Money
	var version int64
	var err error
	if debit {
		before, after, version, err = tx.Wallets().ApplyDebit(ctx, w.ID(), wt.Amount(), now)
		if errors.Is(err, domain.ErrInsufficientFunds) {
			code := wager.FailureInsufficientFunds
			if wt.Kind() == wager.KindRollback {
				code = wager.FailureInsufficientFundsReversal
			}
			if err := wt.MarkRejected(code, now); err != nil {
				return err
			}
			if err := s.saveWager(ctx, tx, wt, insert); err != nil {
				return err
			}
			return s.appendRejected(ctx, tx, wt, corr, now)
		}
		if err != nil {
			return err
		}
	} else {
		before, after, version, err = tx.Wallets().ApplyCredit(ctx, w.ID(), wt.Amount(), now)
		if err != nil {
			return err
		}
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
	return s.appendProcessed(ctx, tx, wt, entry, version, corr, now)
}

func (s *Service) saveWager(ctx context.Context, tx Tx, wt *wager.Transaction, insert bool) error {
	if insert {
		return mapInsertErr(tx.Wagers().Insert(ctx, wt))
	}
	return tx.Wagers().Update(ctx, wt)
}

func (s *Service) appendProcessed(ctx context.Context, tx Tx, wt *wager.Transaction, entry *ledger.Entry, walletVersion int64, corr string, now time.Time) error {
	if err := s.appendOutbox(ctx, tx, events.WagerProcessed(s.ids.New(), wt, corr, now)); err != nil {
		return err
	}
	if entry != nil {
		return s.appendOutbox(ctx, tx, events.BalanceChanged(s.ids.New(), entry, wt.ID(), walletVersion, corr, now))
	}
	return nil
}

func (s *Service) appendRejected(ctx context.Context, tx Tx, wt *wager.Transaction, corr string, now time.Time) error {
	return s.appendOutbox(ctx, tx, events.WagerRejected(s.ids.New(), wt, corr, now))
}

func (s *Service) appendOutbox(ctx context.Context, tx Tx, env events.Envelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return tx.Outbox().Insert(ctx, OutboxRecord{
		ID:          s.ids.New(),
		EventID:     env.EventID,
		EventType:   env.EventType,
		AggregateID: env.AggregateID,
		Payload:     payload,
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

func (s *Service) lookupReplay(ctx context.Context, cmd ProcessWagerCommand) (ProcessResult, error) {
	tx, err := s.uow.Begin(ctx)
	if err != nil {
		return ProcessResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	existing, err := tx.Wagers().GetByIdempotencyKey(ctx, cmd.IdempotencyKey)
	if isNotFound(err) {
		existing, err = tx.Wagers().GetByExternalID(ctx, cmd.ProviderID, cmd.ExternalTransactionID)
	}
	if err != nil {
		return ProcessResult{}, err
	}
	if existing.PayloadHash() != cmd.PayloadHash {
		return ProcessResult{}, domain.ErrIdempotencyConflict
	}
	if existing.IdempotencyKey() != cmd.IdempotencyKey {
		return ProcessResult{}, domain.ErrExternalIDConflict
	}
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

func (s *Service) rejectDuplicate(ctx context.Context, cmd ProcessWagerCommand) (ProcessResult, error) {
	return ProcessResult{}, domain.ErrDuplicateReversal
}

func (s *Service) replay(existing *wager.Transaction) (ProcessResult, error) {
	if existing.ProviderID() == "" && existing.Origin() == wager.OriginExternal {
		return ProcessResult{}, domain.ErrUnauthorizedProvider
	}
	cur := existing.Amount().Currency()
	var bal money.Money
	if existing.ResultBalanceMinor() != nil {
		var err error
		bal, err = money.FromMinor(*existing.ResultBalanceMinor(), cur)
		if err != nil {
			return ProcessResult{}, err
		}
	} else {
		bal, _ = money.Zero(cur)
	}
	return ProcessResult{Transaction: existing, Balance: bal, IdempotentReplay: true}, nil
}

func (s *Service) OpenWallet(ctx context.Context, cmd OpenWalletCommand) (OpenWalletResult, error) {
	if !cmd.InternalService {
		return OpenWalletResult{}, domain.ErrUnauthorizedInternal
	}
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
		if err := s.appendProcessed(ctx, tx, opening, entry, w.Version(), string(opening.ID()), now); err != nil {
			return OpenWalletResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return OpenWalletResult{}, err
	}
	return OpenWalletResult{Wallet: w}, nil
}

func (s *Service) GetWallet(ctx context.Context, id wallet.ID) (*wallet.Wallet, error) {
	tx, err := s.uow.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	w, err := tx.Wallets().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return w, nil
}

func (s *Service) GetTransaction(ctx context.Context, id wager.ID, provider string, internal bool) (*wager.Transaction, error) {
	tx, err := s.uow.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	wt, err := tx.Wagers().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := authorizeRead(wt, provider, internal); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return wt, nil
}

func (s *Service) GetByExternal(ctx context.Context, providerID, externalID, authProvider string) (*wager.Transaction, error) {
	if authProvider != providerID {
		return nil, domain.ErrUnauthorizedProvider
	}
	tx, err := s.uow.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	wt, err := tx.Wagers().GetByExternalID(ctx, providerID, externalID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return wt, nil
}

func (s *Service) ListLedger(ctx context.Context, walletID wallet.ID, after time.Time, afterID string, limit int) ([]*ledger.Entry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	tx, err := s.uow.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	items, err := tx.Ledger().ListByWallet(ctx, walletID, after, afterID, limit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) Reconcile(ctx context.Context, walletID wallet.ID) (ReconcileResult, error) {
	tx, err := s.uow.Begin(ctx)
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
	cmp, err := w.Balance().Cmp(calc)
	if err != nil {
		return ReconcileResult{}, err
	}
	return ReconcileResult{
		WalletID:          walletID,
		StoredBalance:     w.Balance(),
		CalculatedBalance: calc,
		Difference:        diff,
		Consistent:        cmp == 0,
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

func isNotFound(err error) bool {
	return errors.Is(err, domain.ErrWalletNotFound) || errors.Is(err, domain.ErrTransactionNotFound)
}

func mapInsertErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrIdempotencyConflict) || errors.Is(err, domain.ErrDuplicateReversal) || errors.Is(err, domain.ErrExternalIDConflict) || errors.Is(err, domain.ErrWalletConflict) {
		return err
	}
	return err
}

func deref(v *int64, fallback int64) int64 {
	if v == nil {
		return fallback
	}
	return *v
}

func cmdCorrelation(wt *wager.Transaction) string {
	return string(wt.ID())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func FormatError(err error) string {
	return fmt.Sprintf("%v", err)
}
