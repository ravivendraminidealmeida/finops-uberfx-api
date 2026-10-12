package wager

import "junglegaming.io/finops-api/internal/domain"

func (k Kind) RequiresReference() bool {
	return k == KindRefund || k == KindRollback
}

func (k Kind) OptionalReference() bool {
	return k == KindWin
}

func (k Kind) ValidExternal() bool {
	switch k {
	case KindBet, KindWin, KindLoss, KindRefund, KindRollback:
		return true
	default:
		return false
	}
}

func ParseKind(s string) (Kind, error) {
	k := Kind(s)
	if k == KindOpening {
		return "", domain.ErrOpeningNotAllowed
	}
	if !k.ValidExternal() {
		return "", domain.ErrInvalidKind
	}
	return k, nil
}

// DebitsPlayer is the ledger direction of this operation against the player wallet.
func (k Kind) DebitsPlayer(ref Kind) bool {
	switch k {
	case KindBet:
		return true
	case KindWin, KindRefund:
		return false
	case KindRollback:
		return ref == KindWin || ref == KindRefund
	default:
		return false
	}
}
