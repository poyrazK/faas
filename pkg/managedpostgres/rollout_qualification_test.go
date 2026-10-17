package managedpostgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/guestqualification"
)

func rolloutFixture(t *testing.T) (*Registry, *qualificationProvider, QualificationArtifact, guestqualification.Report, time.Time) {
	t.Helper()
	p := &qualificationProvider{capabilities: testCapabilities()}
	r := testRegistry(t, p, func(c *Config) { c.ProvisioningEnabled = true })
	backend, err := r.Default(r.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	report, err := QualifyProvider(context.Background(), p, QualificationOptions{ProviderName: backend.Driver, ResourceID: "rollout", Spec: testSpec(), Mutating: true})
	if err != nil {
		t.Fatal(err)
	}
	now := report.CompletedAt.Add(time.Minute)
	lifecycle := passingLifecycleQualificationReport()
	approval, err := BuildQualificationApproval(report, &lifecycle, backend.ID, backend.Fingerprint, []string{"account-a"}, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	artifact := QualificationArtifact{Version: QualificationArtifactVersion, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Spec: testSpec(), Report: report, Lifecycle: &lifecycle, Approval: &approval}
	guest := guestqualification.Report{Version: guestqualification.Version, SuiteVersion: guestqualification.SuiteContract().Version, RootTest: guestqualification.SuiteContract().Root, HostClass: "native", HostOS: "linux", HostArch: "amd64", SourceClean: true, PostgresMajor: artifact.Spec.PostgresMajor, StartedAt: now.Add(-2 * time.Minute), CompletedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), LeakCheckBefore: true, LeakCheckAfter: true, ResultsSHA256: strings.Repeat("a", 64), Runtime: guestqualification.RuntimeManifest{Version: guestqualification.Version, SourceRevision: strings.Repeat("b", 40), Artifacts: make(map[string]string)}}
	for _, name := range guestqualification.SuiteContract().Artifacts {
		guest.Runtime.Artifacts[name] = strings.Repeat("c", 64)
	}
	guest.Runtime.Artifacts["probe_app_layer"] = strings.Repeat("d", 64)
	guest.Runtime.Artifacts["probe_source_archive"] = strings.Repeat("e", 64)
	guest.Runtime.Artifacts["python312_app_layer"] = strings.Repeat("0", 64)
	guest.Runtime.Artifacts["python312_source_archive"] = strings.Repeat("1", 64)
	for _, phase := range guestqualification.SuiteContract().Phases {
		guest.Checks = append(guest.Checks, guestqualification.PhaseCheck{Name: phase, Passed: true})
	}
	guest.EvidenceSHA256 = guestqualification.EvidenceDigest(guest)
	artifact.SourceRevision = guest.Runtime.SourceRevision
	artifact.SourceClean = true
	artifact.SourceEvidenceSHA256 = QualificationSourceDigest(artifact)
	return r, p, artifact, guest, now
}

func writeRolloutJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRolloutReadinessCombinesEvidenceWithoutProviderCalls(t *testing.T) {
	r, p, artifact, guest, now := rolloutFixture(t)
	before := *p
	result := r.VerifyRolloutQualification(artifact, &guest, &guest.Runtime, []string{"account-a"}, now)
	if !result.Ready || !result.Guest.Ready || !result.ProviderLifecycle.Ready {
		t.Fatalf("combined readiness: %+v", result)
	}
	if p.provision != before.provision || p.inspect != before.inspect || p.usage != before.usage || p.issue != before.issue || p.delete != before.delete {
		t.Fatal("read-only verification contacted provider")
	}
	result = r.VerifyRolloutQualification(artifact, nil, nil, []string{"account-a"}, now)
	if result.Ready || !result.ProviderLifecycle.Ready || result.Guest.Ready {
		t.Fatalf("provider evidence concealed missing guest: %+v", result)
	}
	artifact.BackendFingerprint = "different-provider-placement"
	if r.VerifyRolloutQualification(artifact, &guest, &guest.Runtime, []string{"account-a"}, now).Ready {
		t.Fatal("backend change reused old qualification")
	}
}

func TestRolloutReadinessRejectsLegacyGuestEvidence(t *testing.T) {
	r, _, artifact, guest, now := rolloutFixture(t)
	expected := guest.Runtime
	for _, version := range []int{1, 2} {
		old := guest
		old.Version, old.SuiteVersion, old.Runtime.Version = version, version, version
		old.EvidenceSHA256 = guestqualification.EvidenceDigest(old)
		result := r.VerifyRolloutQualification(artifact, &old, &expected, []string{"account-a"}, now)
		if result.Ready || result.Guest.Ready || !result.ProviderLifecycle.Ready {
			t.Fatalf("legacy guest evidence reused current provider qualification: %+v", result)
		}
	}
}

func TestRolloutProviderProvenanceCannotReuseAnotherRelease(t *testing.T) {
	r, _, artifact, guest, now := rolloutFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*QualificationArtifact)
		resign bool
	}{
		{"old provider revision", func(a *QualificationArtifact) { a.SourceRevision = strings.Repeat("d", 40) }, true},
		{"unstamped provider", func(a *QualificationArtifact) { a.SourceRevision = "" }, true},
		{"dirty provider", func(a *QualificationArtifact) { a.SourceClean = false }, true},
		{"future provider completion", func(a *QualificationArtifact) { a.Report.CompletedAt = now.Add(time.Minute) }, true},
		{"lifecycle changed", func(a *QualificationArtifact) {
			copy := *a.Lifecycle
			copy.Checks = append([]QualificationCheck(nil), copy.Checks...)
			copy.Checks = append(copy.Checks, QualificationCheck{Name: "changed_provenance", Passed: true})
			a.Lifecycle = &copy
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := artifact
			tc.mutate(&candidate)
			if tc.resign {
				candidate.SourceEvidenceSHA256 = QualificationSourceDigest(candidate)
			}
			result := r.VerifyRolloutQualification(candidate, &guest, &guest.Runtime, []string{"account-a"}, now)
			if result.Ready || result.ProviderLifecycle.Ready || !result.Guest.Ready {
				t.Fatalf("provider provenance bypassed: %+v", result)
			}
		})
	}
}

func TestCombinedStagingGateRejectsFallbackAndExpiresAfterStartup(t *testing.T) {
	r, _, artifact, guest, now := rolloutFixture(t)
	dir := t.TempDir()
	providerPath, guestPath, runtimePath := filepath.Join(dir, "provider.json"), filepath.Join(dir, "guest.json"), filepath.Join(dir, "runtime.json")
	writeRolloutJSON(t, providerPath, artifact)
	writeRolloutJSON(t, guestPath, guest)
	writeRolloutJSON(t, runtimePath, guest.Runtime)
	values := map[string]string{EnvironmentEnv: "staging", QualificationApprovalPathEnv: providerPath, GuestQualificationPathEnv: guestPath, RuntimeManifestPathEnv: runtimePath, RequireGuestQualificationEnv: "true", CanaryAccountsEnv: "account-a"}
	getenv := func(key string) string { return values[key] }
	gate := NewStagingProvisioningGate(r, getenv, func() time.Time { return now })
	if !gate() {
		t.Fatal("complete evidence did not open opted-in staging gate")
	}
	for _, tc := range []struct {
		name   string
		mutate func()
	}{
		{"missing guest", func() { delete(values, GuestQualificationPathEnv) }},
		{"missing manifest", func() { delete(values, RuntimeManifestPathEnv) }},
		{"legacy bypass", func() { delete(values, QualificationApprovalPathEnv); values[QualificationEnv] = "true" }},
		{"malformed boolean", func() { values[RequireGuestQualificationEnv] = "invalid" }},
		{"canary changed", func() { values[CanaryAccountsEnv] = "account-b" }},
		{"production", func() { values[EnvironmentEnv] = "production" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := make(map[string]string)
			for k, v := range values {
				saved[k] = v
			}
			tc.mutate()
			if NewStagingProvisioningGate(r, getenv, func() time.Time { return now })() {
				t.Fatal("incomplete evidence opened gate")
			}
			values = saved
		})
	}
	values[RequireGuestQualificationEnv] = "false"
	delete(values, GuestQualificationPathEnv)
	if NewStagingProvisioningGate(r, getenv, func() time.Time { return now })() {
		t.Fatal("false flag bypassed partial guest configuration")
	}
	values[GuestQualificationPathEnv] = guestPath
	guest.HostClass = "nested-diagnostic"
	guest.EvidenceSHA256 = guestqualification.EvidenceDigest(guest)
	writeRolloutJSON(t, guestPath, guest)
	if NewStagingProvisioningGate(r, getenv, func() time.Time { return now })() {
		t.Fatal("nested diagnostics opened gate")
	}
	now = guest.ExpiresAt
	if gate() {
		t.Fatal("cached evidence stayed valid after expiry")
	}
}
