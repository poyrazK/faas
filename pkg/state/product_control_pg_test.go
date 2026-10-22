//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProductControlPostgres(t *testing.T) { state.ProductControlContract(t, state.XNewPgStore(t)) }

// adr: 20261021154511
func TestProductIncludedStartsPostgres(t *testing.T) {
	state.ProductIncludedStartsContract(t, state.XNewPgStore(t))
}

func TestProductControlDeploymentPilotPostgres(t *testing.T) {
	s := state.XNewPgStore(t)
	result := state.ProductDeploymentPilot(t, s)
	if _, err := (*s.Pool()).Exec(t.Context(), "DELETE FROM customer_operations WHERE id=$1", (*result.Operation()).ID); err != nil {
		t.Fatal(err)
	}
	state.CheckProductRetainedReceipt(t, s, result)
}
