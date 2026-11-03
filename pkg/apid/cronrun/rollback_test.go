package cronrun_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/cronrun"
	"github.com/onebox-faas/faas/pkg/state"
)

// A rollback leaves crons alone, so its response names the running HTTP crons
// added after the deployment it rolled back to (crons audit 2026-11-02).
func TestRollbackNotice(t *testing.T) {
	target := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	before := target.Add(-time.Hour)
	after := target.Add(time.Hour)
	crons := []state.Cron{
		{ID: "old", Path: "/jobs/old", Enabled: true, CreatedAt: before},
		{ID: "new", Path: "/jobs/new", Enabled: true, CreatedAt: after},
		{ID: "off", Path: "/jobs/off", CreatedAt: after},
		{ID: "suspended", Path: "/jobs/suspended", Enabled: true, SuspendedReason: "no_live_deployment", CreatedAt: after},
		{ID: "command", Command: []string{"bin/task"}, Enabled: true, CreatedAt: after},
	}
	got := cronrun.RollbackNotice(crons, target)
	if !strings.Contains(got, "1 cron(s)") || !strings.Contains(got, "POST /jobs/new") {
		t.Fatalf("notice = %q, want the one running HTTP cron added after the target", got)
	}
	for _, absent := range []string{"/jobs/old", "/jobs/off", "/jobs/suspended", `"`} {
		if strings.Contains(got, absent) {
			t.Fatalf("notice %q names %q", got, absent)
		}
	}
	if got := cronrun.RollbackNotice(crons[:1], target); got != "" {
		t.Fatalf("notice with no cron added = %q, want none", got)
	}

	var many []state.Cron
	for i := range 5 {
		many = append(many, state.Cron{ID: fmt.Sprint(i), Path: fmt.Sprintf(`/jobs/"%d"`, i), Enabled: true, CreatedAt: after})
	}
	got = cronrun.RollbackNotice(many, target)
	if !strings.Contains(got, "5 cron(s)") || !strings.Contains(got, "and 2 more") || strings.Contains(got, `"`) {
		t.Fatalf("notice for five crons = %q, want three paths, the rest counted, no quotes", got)
	}
}
