package state

import (
	"os"
	"strings"
	"testing"
)

// TestRoutePolicyAccountLockLeavesForeignKeyChecks keeps the route policy
// snapshot's account lock off FOR UPDATE. The snapshot locks the namespace and
// then the app; a child insert's foreign-key checks take KEY SHARE on the app
// and then the namespace, so a FOR UPDATE namespace lock deadlocked a traffic
// split against the gateway's request-ID journal (nightly 2026-11-25).
func TestRoutePolicyAccountLockLeavesForeignKeyChecks(t *testing.T) {
	raw, err := os.ReadFile("queries.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(raw), "-- name: LockRoutePolicyAccount :one\n")
	if !ok {
		t.Fatal("LockRoutePolicyAccount is missing from queries.sql")
	}
	query, _, _ := strings.Cut(rest, "-- name: ")
	var body []string
	for _, line := range strings.Split(query, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			body = append(body, line)
		}
	}
	sql := strings.ToUpper(strings.Join(body, "\n"))
	if strings.Contains(sql, "FOR UPDATE") {
		t.Fatal("LockRoutePolicyAccount locks the namespace or account FOR UPDATE; use FOR NO KEY UPDATE")
	}
	if strings.Count(sql, "FOR NO KEY UPDATE") != 2 {
		t.Fatal("LockRoutePolicyAccount must still lock the namespace and the legacy account row")
	}
}
