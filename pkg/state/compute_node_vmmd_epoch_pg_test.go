//go:build !no_pg

package state

import "testing"

func TestPgVMMDEpoch(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	vmmdEpochLifecycle(t, s)
}

func TestPgAdoptionReportInstances(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	adoptionReportInstancesLifecycle(t, s)
}

func TestPgVMMDRestartNotice(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	vmmdRestartNoticeLifecycle(t, s)
}
