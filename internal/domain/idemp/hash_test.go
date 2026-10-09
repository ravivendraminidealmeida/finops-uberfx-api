package idemp_test

import (
	"testing"

	"junglegaming.io/finops-api/internal/domain/idemp"
)

func TestCanonicalHashStable(t *testing.T) {
	a := idemp.BusinessFields("p", "e", "pl", "w", "r", "g", "BET", "25.00", "BRL", "")
	b := idemp.BusinessFields("p", "e", "pl", "w", "r", "g", "BET", "25.00", "BRL", "")
	ha, err := idemp.CanonicalHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := idemp.CanonicalHash(b)
	if ha != hb || ha == "" {
		t.Fatalf("hashes %s %s", ha, hb)
	}
	c := idemp.BusinessFields("p", "e", "pl", "w", "r", "g", "WIN", "25.00", "BRL", "")
	hc, _ := idemp.CanonicalHash(c)
	if hc == ha {
		t.Fatal("different payload produced same hash")
	}
}
