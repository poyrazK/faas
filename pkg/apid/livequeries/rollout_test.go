package livequeries

import (
	"net/http/httptest"
	"testing"
)

// adr: 1384 — FAAS_LIVE_QUERIES_ACCOUNTS: nobody by default, the listed
// accounts, or everyone; live paths stay hidden from 405s until everyone.
func TestRollout(t *testing.T) {
	const ours = "6F1C2B4A-0D3E-4F5A-8B9C-0123456789AB"
	off, err := ParseRollout("")
	if err != nil || !off.Off() || off.Allows(ours) {
		t.Fatalf("empty rollout = %+v, %v", off, err)
	}
	listed, err := ParseRollout(" " + ours + " ,")
	if err != nil || listed.Off() || !listed.Allows("6f1c2b4a-0d3e-4f5a-8b9c-0123456789ab") ||
		listed.Allows("00000000-0000-0000-0000-000000000001") || listed.Allows("not-a-uuid") {
		t.Fatalf("listed rollout = %+v, %v", listed, err)
	}
	all, err := ParseRollout("*")
	if err != nil || !all.Allows("anything") {
		t.Fatalf("* rollout = %+v, %v", all, err)
	}
	if _, err := ParseRollout(ours + ",acct-1"); err == nil {
		t.Fatal("a non-UUID entry was accepted")
	}

	for path, live := range map[string]bool{
		"/v1/postgres/databases/db_1/live":        true,
		"/v1/postgres/databases/db_1/live/rotate": true,
		"/v1/postgres/databases/db_1/bindings":    false,
		"/v1/postgres/databases//live":            false,
		"/v1/postgres/databases/db_1/live/other":  false,
	} {
		if IsLivePath(path) != live {
			t.Errorf("IsLivePath(%q) = %v", path, !live)
		}
		r := httptest.NewRequest("PUT", path, nil)
		if listed.Hidden(r) != live || all.Hidden(r) {
			t.Errorf("Hidden(%q): listed %v, all %v", path, listed.Hidden(r), all.Hidden(r))
		}
	}
}
