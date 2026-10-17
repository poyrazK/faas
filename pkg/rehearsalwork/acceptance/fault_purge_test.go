package acceptance

import "testing"

func TestWorkerTerminationRetainsEvidenceUntilParentPurge(t *testing.T) {
	f := newFixture(t, "postgres", false, true)
	f.claim(t, f.enqueue(t).ID)
	d := f.checkpoint(t)
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM scenario_worker_faults WHERE id=$1", d.WorkerFaultID); err == nil {
		t.Fatal("unsealed fault receipt deleted directly")
	}
	// Retire this private test app's dependencies before its parent purge, as
	// the platform does. Unsealed fault evidence must not block that cascade.
	for _, query := range []string{
		"DELETE FROM invocations WHERE app_id=$1",
		"DELETE FROM instances WHERE app_id=$1",
		"DELETE FROM deployments WHERE app_id=$1",
		"DELETE FROM apps WHERE id=$1",
	} {
		if _, err := f.pool.Exec(t.Context(), query, f.apps["worker"].ID); err != nil {
			t.Fatalf("parent purge: %s: %v", query, err)
		}
	}
	var retained int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM scenario_worker_faults WHERE id=$1", d.WorkerFaultID).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("parent purge retained fault evidence: %d %v", retained, err)
	}
}
