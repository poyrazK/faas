package journeys

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// budgetNamePrefix names the budgets this suite creates, so a later run can
// remove the ones a killed run left behind.
const budgetNamePrefix = "e2e budget "

// TestBudgets walks the GA budget surface (row 152, ADR-20261107160000) with
// a monitored notification budget: preview, create, list, get, update,
// revision history, alert channels and delete. Strict budgets stay beta and
// are not created here: one would stop the account's workloads.
func TestBudgets(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	s := newStory(t)
	key := "e2e-" + tg.runID + "-budget"
	// The thresholds sit far above a journey account's month, so no alert
	// email is sent.
	spec := map[string]any{
		"name":              budgetNamePrefix + tg.runID,
		"scope":             map[string]any{"kind": "account"},
		"currency":          "USD",
		"meters":            []string{"compute"},
		"basis":             "net_usage",
		"limit_millicents":  100_000_000,
		"notify_millicents": []int64{80_000_000},
		"mode":              "monitored",
		"action":            "notify",
		"drain_seconds":     0,
		"resume_rule":       "next_period",
		"enabled":           true,
	}
	file := writeBudgetSpec(t, spec)
	var id string
	revision := 0
	t.Cleanup(func() {
		if id != "" {
			tg.run(t, time.Minute, "billing", "budgets", "delete", "--expected-revision", strconv.Itoa(revision), "--key", key+"-cleanup", "--json", id)
		}
	})

	s.check("sweep_stale", func(t *testing.T) { tg.sweepBudgets(t) })
	s.step("preview", func(t *testing.T) {
		preview := object(t, tg.ok(t, time.Minute, "billing", "budget-preview", "--file", file, "--json").stdout)
		if str(preview, "spec.name") != spec["name"] || field(preview, "enforcement_ready") != false || str(preview, "period_start") == "" {
			t.Fatalf("notify budget preview = %s", compact(preview))
		}
	})
	s.step("create", func(t *testing.T) {
		created := object(t, tg.ok(t, time.Minute, "billing", "budgets", "create", "--file", file, "--key", key+"-create", "--json").stdout)
		id, revision = str(created, "id"), int(num(created, "revision"))
		if id == "" || revision != 1 || str(created, "status") != "active" ||
			field(created, "notification_ready") != true || field(created, "enforcement_ready") != false {
			t.Fatalf("created notify budget = %s", compact(created))
		}
	})
	s.check("list", func(t *testing.T) {
		listed := object(t, tg.ok(t, time.Minute, "billing", "budgets", "list", "--json").stdout)
		budgets, _ := field(listed, "budgets").([]any)
		for _, b := range budgets {
			if str(b, "id") == id {
				return
			}
		}
		t.Fatalf("budget %s missing from %d listed budgets", id, len(budgets))
	})
	s.check("get", func(t *testing.T) {
		got := object(t, tg.ok(t, time.Minute, "billing", "budgets", "get", "--json", id).stdout)
		if str(got, "id") != id || str(got, "status") != "active" || str(got, "spec.action") != "notify" {
			t.Fatalf("budget get = %s", compact(got))
		}
	})
	s.step("update", func(t *testing.T) {
		spec["limit_millicents"] = 200_000_000
		updated := object(t, tg.ok(t, time.Minute, "billing", "budgets", "update", "--file", writeBudgetSpec(t, spec),
			"--expected-revision", strconv.Itoa(revision), "--key", key+"-update", "--json", id).stdout)
		if int(num(updated, "revision")) != revision+1 || num(updated, "spec.limit_millicents") != 200_000_000 {
			t.Fatalf("budget update = %s", compact(updated))
		}
		revision++
	})
	s.check("history", func(t *testing.T) {
		history := object(t, tg.ok(t, time.Minute, "billing", "budgets", "history", "--json", id).stdout)
		revisions, _ := field(history, "revisions").([]any)
		if len(revisions) != revision || num(revisions[0], "revision") != 1 || num(revisions[len(revisions)-1], "revision") != float64(revision) {
			t.Fatalf("budget history = %s; want revisions 1..%d", compact(history), revision)
		}
	})
	s.check("channels", func(t *testing.T) {
		channels := object(t, tg.ok(t, time.Minute, "billing", "budgets", "channels", "--json", id).stdout)
		if str(channels, "policy_id") != id {
			t.Fatalf("budget channels = %s", compact(channels))
		}
		cleared := object(t, tg.ok(t, time.Minute, "billing", "budgets", "channels", "--clear", "--json", id).stdout)
		if list, _ := field(cleared, "channels").([]any); str(cleared, "policy_id") != id || len(list) != 0 {
			t.Fatalf("cleared budget channels = %s", compact(cleared))
		}
	})
	s.step("delete", func(t *testing.T) {
		deleted := object(t, tg.ok(t, time.Minute, "billing", "budgets", "delete", "--expected-revision", strconv.Itoa(revision),
			"--key", key+"-delete", "--json", id).stdout)
		if str(deleted, "id") != id || str(deleted, "status") != "deleted" || str(deleted, "deleted_at") == "" {
			t.Fatalf("budget delete = %s", compact(deleted))
		}
		id = ""
	})
}

func writeBudgetSpec(t *testing.T, spec map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "budget.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// sweepBudgets deletes budgets an earlier run left behind: named by this
// suite, from another run, and over six hours old. An account holds at most
// 128 budgets.
func (tg *target) sweepBudgets(t *testing.T) {
	t.Helper()
	listed := object(t, tg.ok(t, time.Minute, "billing", "budgets", "list", "--json").stdout)
	budgets, _ := field(listed, "budgets").([]any)
	for _, b := range budgets {
		run, ok := strings.CutPrefix(str(b, "spec.name"), budgetNamePrefix)
		started, valid := runTime(run)
		if !ok || !valid || run == tg.runID || time.Since(started) < 6*time.Hour || str(b, "status") == "deleted" {
			continue
		}
		res := tg.run(t, time.Minute, "billing", "budgets", "delete", "--expected-revision", str(b, "revision"),
			"--key", "e2e-sweep-"+str(b, "id"), "--json", str(b, "id"))
		if res.exit != 0 {
			t.Logf("warning: could not delete stale budget %s: %s", str(b, "id"), tg.redact(res.problem()))
		}
	}
}
