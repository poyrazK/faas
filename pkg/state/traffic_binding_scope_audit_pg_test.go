//go:build !no_pg

// adr: 20261027163626
package state

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

// The binding writer suites, run under an audit observer: every hostname a
// write changes must lie inside the scope it declared, or the scoped guard
// would analyse and lock too little.
func TestPgTrafficBindingScopeCoversWriters(t *testing.T) {
	var mu sync.Mutex
	audits, misses := map[string]int{}, map[string]int{}
	SetTrafficBindingScopeObserver(func(writer string, missed []string) {
		mu.Lock()
		defer mu.Unlock()
		audits[writer]++
		for _, kind := range missed {
			misses[writer+"/"+kind]++
		}
	})
	t.Cleanup(func() { SetTrafficBindingScopeObserver(nil) })
	for name, suite := range map[string]func(*testing.T){
		"app withdrawals":        TestPgTrafficAppBindingWithdrawals,
		"app rename":             TestPgTrafficAppRenamePublication,
		"app purge reservations": TestPgTrafficAppPurgeGlobalReservations,
		"app batch withdrawal":   TestPgTrafficAppBatchWithdrawal,
		"alias withdrawal":       TestPgTrafficAliasWithdrawal,
		"alias revival":          TestPgTrafficAliasDeploymentRevivalRollback,
		"rollback revival":       TestPgTrafficRollbackRevivalPublication,
		"domain publication":     TestPgTrafficDomainMetadataAndAppPublication,
		"scoped domain":          TestPgTrafficScopedDomainVerification,
		"tenant transitions":     TestPgTrafficTenantCompleteTransitions,
		"environment removal":    TestPgTrafficEnvironmentRemoval,
		"account retirement":     TestPgTrafficAccountRetirement,
	} {
		t.Run(name, suite)
	}
	mu.Lock()
	defer mu.Unlock()
	writers := make([]string, 0, len(audits))
	for writer, count := range audits {
		writers = append(writers, fmt.Sprintf("%s=%d", writer, count))
	}
	sort.Strings(writers)
	t.Logf("audited binding writes: %v", writers)
	if len(misses) > 0 {
		t.Fatalf("changed hostnames outside the declared scope (writer/kind=count): %v", misses)
	}
}
