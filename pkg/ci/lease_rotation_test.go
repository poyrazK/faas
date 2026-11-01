package ci

import "testing"

// forgetJobAttempt lets a test hand a claimed task a new lease on the same
// instance. Since 20261017190351248_financial_compute_jobs the
// financial_compute_job_task_guard trigger records each claim's physical
// attempt and refuses to reuse an instance under another lease; a real
// re-claim starts a new attempt, so the test drops the recorded one first.
func forgetJobAttempt(t *testing.T, h *jobsHarness, runID string) {
	t.Helper()
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM financial_compute_job_attempts WHERE run_id=$1", runID); err != nil {
		t.Fatal(err)
	}
}
