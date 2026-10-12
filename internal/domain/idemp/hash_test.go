package idemp_test

import (
	"testing"

	"junglegaming.io/finops-api/internal/domain/idemp"
)

func TestPayloadHashStable(t *testing.T) {
	a := idemp.Payload{ProviderID: "p", ExternalTransactionID: "e", PlayerID: "pl", WalletID: "w", RoundID: "r", GameID: "g", Kind: "BET", Amount: "25.00", Currency: "BRL"}
	b := a
	ha, err := a.Hash()
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := b.Hash()
	if ha != hb || ha == "" {
		t.Fatalf("hashes %s %s", ha, hb)
	}
	c := a
	c.Kind = "WIN"
	hc, _ := c.Hash()
	if hc == ha {
		t.Fatal("different payload produced same hash")
	}
}
