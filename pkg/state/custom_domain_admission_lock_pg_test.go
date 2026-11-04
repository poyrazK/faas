package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// A row-lock wait must not preserve the allowlist snapshot from before the
// writer committed. Read permission after acquiring the claim lock.
func TestCustomDomainTLSAdmissionReadsAllowlistAfterLockWait(t *testing.T) {
	store, ctx := pgStore(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	app := tlsHostApp(t, ctx, store, "")
	const wildcard = "*.locked-admission.example.com"
	verifiedTLSWildcard(t, ctx, store, wildcard, app)
	claim, err := store.DomainByName(ctx, wildcard)
	if err != nil {
		t.Fatal(err)
	}
	pool := *store.Pool()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var marker int
	if err = tx.QueryRow(ctx, "SELECT 1 FROM custom_domains WHERE domain=$1 FOR NO KEY UPDATE", wildcard).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	type result struct {
		verdict state.CustomDomainTLSVerdict
		err     error
	}
	done := make(chan result, 1)
	a := roomyTLSAdmission(wildcard, "live.locked-admission.example.com", time.Now())
	a.ExpectedAppID, a.ExpectedToken = claim.AppID, claim.ChallengeToken
	go func() { verdict, err := store.AdmitCustomDomainTLSHost(ctx, a); done <- result{verdict, err} }()
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '-- name: LockCustomDomainTLSAdmissionClaim%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case early := <-done:
			t.Fatalf("admission did not wait for writer: %+v", early)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if _, err = tx.Exec(ctx, "INSERT INTO custom_domain_tls_allowed_hosts(wildcard_domain,name) VALUES($1,'different-host')", wildcard); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.verdict != state.TLSHostNotAllowlisted {
			t.Fatalf("pre-lock permission survived revocation: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
