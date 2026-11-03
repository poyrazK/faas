//go:build !no_pg

package migrations_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainTLSExactActivationPreservesExistingCertificates(t *testing.T) {
	pool := openMigrated(t)
	ctx := t.Context()
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	// Reconstruct the pre-activation state: broader constraints have been
	// validated, while the original wildcard-only checks still apply.
	if _, err := pool.Exec(ctx, `
		ALTER TABLE custom_domain_tls_hosts
		 ADD CONSTRAINT custom_domain_tls_hosts_wildcard_chk CHECK (wildcard_domain LIKE '*.%'),
		 ADD CONSTRAINT custom_domain_tls_hosts_host_chk CHECK (host NOT LIKE '%*%' AND host LIKE '%' || substr(wildcard_domain, 2));
		ALTER TABLE custom_domain_tls_admissions
		 ADD CONSTRAINT custom_domain_tls_admissions_host_chk CHECK (host NOT LIKE '%*%' AND host LIKE '%.' || registered_domain)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO custom_domains (domain, app_id, challenge_token, verified_at)
		 VALUES ('existing.example.com', $1, 'verified', now()),
		        ('pending.example.com', $1, 'pending', NULL),
		        ('*.example.com', $1, 'drifted-wildcard', NULL),
		        ('*.example.net', $1, 'wildcard', now())`, app); err != nil {
		t.Fatal(err)
	}
	// A previous wildcard admission must be rebound to the verified exact
	// claim, even if that wildcard has since lost verification.
	if _, err := pool.Exec(ctx, `INSERT INTO custom_domain_tls_hosts (host, wildcard_domain, admitted_at)
		VALUES ('existing.example.com', '*.example.com', now())`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range []string{
		"20261103232750058_custom_domain_tls_exact_activation.sql",
		"20261103232750064_custom_domain_tls_existing_exact_hosts.sql",
	} {
		source, err := os.ReadFile(migration)
		if err != nil {
			t.Fatal(err)
		}
		up, _, ok := strings.Cut(string(source), "-- +goose Down")
		if !ok {
			t.Fatal("missing migration downgrade marker")
		}
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatal(err)
		}
	}
	var known, charged int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM custom_domain_tls_hosts`).Scan(&known); err != nil || known != 1 {
		t.Fatalf("grandfathered exact hosts=%d, %v; want only existing.example.com", known, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM custom_domain_tls_admissions`).Scan(&charged); err != nil || charged != 0 {
		t.Fatalf("legacy hosts consumed budget: %d, %v", charged, err)
	}
	store := state.NewPgStore(pool)
	verdict, err := store.AdmitCustomDomainTLSHost(ctx, state.CustomDomainTLSAdmission{
		Wildcard: "existing.example.com", Host: "existing.example.com", Now: time.Now(),
		Window: time.Hour, FleetWindow: time.Hour,
	})
	if err != nil || verdict != state.TLSHostAdmitted {
		t.Fatalf("existing certificate denied with full budgets: %v, %v", verdict, err)
	}
}
