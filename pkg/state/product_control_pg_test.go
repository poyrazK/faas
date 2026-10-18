//go:build !no_pg

package state

import "testing"

func TestProductControlPostgres(t *testing.T) { productControlContract(t, newPgStore(t)) }

func TestProductControlDeploymentPilotPostgres(t *testing.T) {
	s := newPgStore(t)
	result := productDeploymentPilot(t, s)
	if _, err := s.pool.Exec(t.Context(), "DELETE FROM customer_operations WHERE id=$1", result.operation.ID); err != nil {
		t.Fatal(err)
	}
	checkProductRetainedReceipt(t, s, result)
}
