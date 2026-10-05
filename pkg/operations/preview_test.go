// adr: 521 — bounded preview intent never changes admitted execution authority.
package operations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperationPreviewPolicyBoundsAndOwnership(t *testing.T) {
	now := time.Now().UTC()
	base := PreviewPolicy{Version: 1, Enabled: true, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), Cohorts: []PreviewCohort{{AccountID: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", AppID: uuid.NewString(), Scope: api.DefaultEnvScope, PlatformTenantIDs: []string{uuid.NewString()}}}}
	path := filepath.Join(t.TempDir(), "preview.json")
	gate, err := NewPreviewAdmission(path)
	if err != nil {
		t.Fatal(err)
	}
	gate.Now = func() time.Time { return now }
	write := func(p PreviewPolicy) {
		t.Helper()
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(base)
	c := base.Cohorts[0]
	if !gate.AllowsDefinition(c.AccountID, c.AppID, c.Scope) || !gate.AllowsTenant(c.AccountID, c.AppID, c.Scope, c.PlatformTenantIDs[0]) {
		t.Fatal("exact preview cohort denied")
	}
	if !gate.AllowsTenant(strings.ReplaceAll(c.AccountID, "-", ""), strings.ReplaceAll(c.AppID, "-", ""), c.Scope, c.PlatformTenantIDs[0]) {
		t.Fatal("store UUID encoding changed cohort identity")
	}
	for _, tuple := range [][4]string{{uuid.NewString(), c.AppID, c.Scope, c.PlatformTenantIDs[0]}, {c.AccountID, uuid.NewString(), c.Scope, c.PlatformTenantIDs[0]}, {c.AccountID, c.AppID, "staging", c.PlatformTenantIDs[0]}, {c.AccountID, c.AppID, c.Scope, uuid.NewString()}, {c.AccountID, c.AppID, c.Scope, "invalid"}, {c.AccountID, c.AppID, c.Scope, ""}} {
		if gate.AllowsTenant(tuple[0], tuple[1], tuple[2], tuple[3]) {
			t.Fatal("cohort identity escaped its exact binding")
		}
	}
	cases := []struct {
		name   string
		change func(*PreviewPolicy)
	}{
		{"disabled", func(p *PreviewPolicy) { p.Enabled = false }},
		{"unknown version", func(p *PreviewPolicy) { p.Version++ }},
		{"expired at boundary", func(p *PreviewPolicy) { p.ExpiresAt = now }},
		{"not yet open", func(p *PreviewPolicy) { p.NotBefore = now.Add(time.Second) }},
		{"unbounded window", func(p *PreviewPolicy) { p.ExpiresAt = p.NotBefore.Add(api.OperationPreviewWindowMax + time.Second) }},
		{"missing window", func(p *PreviewPolicy) { p.NotBefore = time.Time{} }},
		{"non UTC window", func(p *PreviewPolicy) { p.NotBefore = p.NotBefore.In(time.FixedZone("other", 3600)) }},
		{"no cohorts", func(p *PreviewPolicy) { p.Cohorts = nil }},
		{"wildcard account", func(p *PreviewPolicy) { p.Cohorts[0].AccountID = "*" }},
		{"nil app", func(p *PreviewPolicy) { p.Cohorts[0].AppID = uuid.Nil.String() }},
		{"wildcard scope", func(p *PreviewPolicy) { p.Cohorts[0].Scope = "*" }},
		{"no customers", func(p *PreviewPolicy) { p.Cohorts[0].PlatformTenantIDs = nil }},
		{"duplicate customers", func(p *PreviewPolicy) {
			p.Cohorts[0].PlatformTenantIDs = append(p.Cohorts[0].PlatformTenantIDs, p.Cohorts[0].PlatformTenantIDs[0])
		}},
		{"uppercase UUID", func(p *PreviewPolicy) { p.Cohorts[0].AccountID = strings.ToUpper(p.Cohorts[0].AccountID) }},
		{"duplicate cohorts", func(p *PreviewPolicy) { p.Cohorts = append(p.Cohorts, p.Cohorts[0]) }},
		{"cohort bound", func(p *PreviewPolicy) {
			for len(p.Cohorts) <= api.OperationPreviewCohortsMax {
				c := p.Cohorts[0]
				c.AppID = uuid.NewString()
				p.Cohorts = append(p.Cohorts, c)
			}
		}},
		{"customer bound", func(p *PreviewPolicy) {
			for len(p.Cohorts[0].PlatformTenantIDs) <= api.OperationPreviewTenantsPerCohortMax {
				p.Cohorts[0].PlatformTenantIDs = append(p.Cohorts[0].PlatformTenantIDs, uuid.NewString())
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var policy PreviewPolicy
			if json.Unmarshal(raw, &policy) != nil {
				t.Fatal("clone policy")
			}
			tc.change(&policy)
			write(policy)
			if gate.AllowsDefinition(c.AccountID, c.AppID, c.Scope) || gate.AllowsTenant(c.AccountID, c.AppID, c.Scope, c.PlatformTenantIDs[0]) {
				t.Fatal("invalid/closed policy retained open authority")
			}
		})
	}
	for _, raw := range []string{`{"version":1,"enabled":true,"enabled":false}`, `{"version":1,"enabled":true,"all_customers":true}`, `null`, `{"version":1,"enabled":`, strings.Repeat(" ", api.OperationPreviewPolicyMaxBytes+1)} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if gate.AllowsDefinition(c.AccountID, c.AppID, c.Scope) {
			t.Fatal("malformed policy retained admission")
		}
	}
	write(base)
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if gate.AllowsDefinition(c.AccountID, c.AppID, c.Scope) {
		t.Fatal("writable policy granted admission")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if gate.AllowsDefinition(c.AccountID, c.AppID, c.Scope) {
		t.Fatal("removed policy retained authority")
	}
	closed, err := NewPreviewAdmission("")
	if err != nil || closed.AllowsDefinition(c.AccountID, c.AppID, c.Scope) {
		t.Fatal("default admission was open")
	}
	if _, err := NewPreviewAdmission("relative.json"); err == nil {
		t.Fatal("relative policy path accepted")
	}
}

func TestOperationPreviewConcurrentAtomicRollback(t *testing.T) {
	now := time.Now().UTC()
	cohort := PreviewCohort{AccountID: uuid.NewString(), AppID: uuid.NewString(), Scope: api.DefaultEnvScope, PlatformTenantIDs: []string{uuid.NewString()}}
	policy := PreviewPolicy{Version: 1, Enabled: true, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), Cohorts: []PreviewCohort{cohort}}
	path := filepath.Join(t.TempDir(), "preview.json")
	gate, err := NewPreviewAdmission(path)
	if err != nil {
		t.Fatal(err)
	}
	replace := func(policy PreviewPolicy) {
		t.Helper()
		raw, _ := json.Marshal(policy)
		stage := path + ".next"
		if err := os.WriteFile(stage, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(stage, path); err != nil {
			t.Fatal(err)
		}
	}
	replace(policy)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 25 {
				if gate.AllowsTenant(cohort.AccountID, cohort.AppID, cohort.Scope, uuid.NewString()) {
					t.Error("concurrent read widened cohort")
				}
				_ = gate.AllowsTenant(cohort.AccountID, cohort.AppID, cohort.Scope, cohort.PlatformTenantIDs[0])
			}
		}()
	}
	policy.Enabled = false
	replace(policy)
	workers.Wait()
	if gate.AllowsTenant(cohort.AccountID, cohort.AppID, cohort.Scope, cohort.PlatformTenantIDs[0]) {
		t.Fatal("atomic rollback retained open policy")
	}
	policy.Enabled = true
	replace(policy)
	if !gate.AllowsTenant(cohort.AccountID, cohort.AppID, cohort.Scope, cohort.PlatformTenantIDs[0]) {
		t.Fatal("reenabling exact cohort required a restart")
	}
}

func TestOperationPreviewObservationSharesAdmissionDecision(t *testing.T) {
	// adr: 521 — diagnostics must use the same current-file decision as admission.
	now := time.Now().UTC()
	account, app, tenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	path := filepath.Join(t.TempDir(), "preview.json")
	gate, _ := NewPreviewAdmission(path)
	gate.Now = func() time.Time { return now }
	p := PreviewPolicy{Version: 1, Enabled: true, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), Cohorts: []PreviewCohort{{AccountID: account, AppID: app, Scope: "production", PlatformTenantIDs: []string{tenant}}}}
	cases := []struct {
		code   string
		mutate func(*PreviewPolicy)
	}{{"preview_cohort_observed", func(*PreviewPolicy) {}}, {"preview_disabled", func(p *PreviewPolicy) { p.Enabled = false }}, {"preview_not_started", func(p *PreviewPolicy) { p.NotBefore = now.Add(time.Second) }}, {"preview_expired", func(p *PreviewPolicy) { p.ExpiresAt = now }}, {"preview_cohort_excluded", func(p *PreviewPolicy) { p.Cohorts[0].PlatformTenantIDs = []string{uuid.NewString()} }}}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			raw, _ := json.Marshal(p)
			var policy PreviewPolicy
			if json.Unmarshal(raw, &policy) != nil {
				t.Fatal("fixture")
			}
			tc.mutate(&policy)
			raw, _ = json.Marshal(policy)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			o := gate.Observe(account, app, "production", tenant)
			if o.Code != tc.code || o.Allowed != gate.AllowsTenant(account, app, "production", tenant) || o.ObservedAt != now {
				t.Fatal("diagnostic/admission disagreement", o)
			}
		})
	}
	if err := os.WriteFile(path, []byte("private-path-invalid-policy"), 0600); err != nil {
		t.Fatal(err)
	}
	if o := gate.Observe(account, app, "production", tenant); o.Code != "preview_policy_unavailable" || o.Allowed {
		t.Fatal(o)
	}
	var absent *PreviewAdmission
	if o := absent.Observe(account, app, "production", tenant); o.Allowed || o.Code != "preview_not_configured" {
		t.Fatal(o)
	}
}
