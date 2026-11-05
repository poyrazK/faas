// adr: 192 — restore sub-phases are split for attribution; this exposes the
// operator-only ones as a histogram instead of a Debug log line.

package fcvm

import "testing"

// Production-us hunt #9: stage_drives (52-61 ms) and stage_snapshot (20-24 ms)
// could not be attributed because their sub-phases and the pre-boot skip
// outcome only reached a Debug log.
func TestObserveRestorePhases(t *testing.T) {
	wpm := NewWakePhaseMetrics()
	wpm.ObserveRestorePhases(restoreTimingBreakdown{StageDrivesMs: 58, StageWritableMs: 9, PreBootJoinWaitMs: 13, NetworkWaitMs: 4, ResumeHookMs: 210, JournalSyncMs: 85, JournalLockWaitMs: 12, JournalCommits: 26})
	wpm.ObserveRestorePhases(restoreTimingBreakdown{StageDrivesMs: 60, PreBootSkipped: true, JournalSyncMs: 3, JournalCommits: 2})
	var nilMetrics *WakePhaseMetrics
	nilMetrics.ObserveRestorePhases(restoreTimingBreakdown{StageDrivesMs: 1}) // nil-safe

	families, err := wpm.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	sums := map[string]float64{}
	counts := map[string]uint64{}
	outcomes := map[string]float64{}
	var commits float64
	for _, f := range families {
		for _, m := range f.GetMetric() {
			label := ""
			for _, l := range m.GetLabel() {
				label = l.GetValue()
			}
			switch f.GetName() {
			case "vmmd_restore_phase_duration_seconds":
				sums[label] = m.GetHistogram().GetSampleSum()
				counts[label] = m.GetHistogram().GetSampleCount()
			case "vmmd_restore_pre_boot_files_total":
				outcomes[label] = m.GetCounter().GetValue()
			case "vmmd_restore_journal_commits_total":
				commits = m.GetCounter().GetValue()
			}
		}
	}
	if len(counts) != len(restorePhaseNames) {
		t.Fatalf("phases = %d, want the closed set of %d", len(counts), len(restorePhaseNames))
	}
	for phase, want := range map[string]float64{"stage_drives": 0.118, "stage_writable": 0.009, "pre_boot_join_wait": 0.013, "network_wait": 0.004, "resume_hook": 0.21, "journal_sync": 0.088, "journal_lock_wait": 0.012} {
		if got := sums[phase]; got < want-1e-9 || got > want+1e-9 || counts[phase] != 2 {
			t.Errorf("%s: sum=%v count=%d, want sum=%v count=2", phase, got, counts[phase], want)
		}
	}
	if commits != 28 {
		t.Fatalf("journal commits = %v, want 28", commits)
	}
	if outcomes["skipped"] != 1 || outcomes["written"] != 1 {
		t.Fatalf("pre-boot outcomes = %v, want skipped=1 written=1", outcomes)
	}
}
