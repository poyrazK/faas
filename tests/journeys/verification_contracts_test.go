package journeys

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Verification contracts (ADR-20261122220000) are in preview: apid serves
// them only to accounts the verification-contracts capability admits. The
// journey skips while the account's capabilities report it hidden.

// verifyJourney is the state TestVerificationContracts's steps share.
type verifyJourney struct {
	tg    *target
	app   string
	base  string
	token string
}

// TestVerificationContracts is a customer's gregale.verify.yaml journey:
// a contract that passes promotes the release, with a state-changing check
// skipped in production and an app secret sent in a header; a failing check
// blocks the release while the previous one keeps serving; a header that
// names an unset secret blocks it with that secret's name.
func TestVerificationContracts(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	skipHidden(t, hiddenCapabilities(t, tg), "verification-contracts")
	j := &verifyJourney{tg: tg}
	j.app = tg.app(t, "verify")
	j.base = tg.appURL(j.app)
	j.token = "e2e-verify-" + tg.runID
	s := newStory(t)
	s.step("passing_contract_promotes", j.passingContractPromotes)
	s.check("contract_stored_on_the_app", j.contractStored)
	s.step("failing_check_blocks", j.failingCheckBlocks)
	s.check("previous_release_serves", func(t *testing.T) { expectVersion(t, j.base, "v1", time.Minute) })
	s.step("unset_secret_blocks", j.unsetSecretBlocks)
}

// verifyContract renders the journey's contract. bodyWant is what / must
// contain; secret names the header's secret.
func (j *verifyJourney) verifyContract(bodyWant, secret string) string {
	sum := sha256.Sum256([]byte("Bearer " + j.token))
	return `version: 1
timeout_seconds: 60
checks:
  - name: serves
    http:
      path: /
      expect:
        status: 200
        body_contains: ["` + bodyWant + `"]
  - name: authorized
    http:
      path: /outbound-echo/verify
      headers:
        Authorization: "Bearer ${secret:` + secret + `}"
      expect:
        json:
          /authorization_sha256: "` + hex.EncodeToString(sum[:]) + `"
  - name: cron-delivery
    http:
      method: POST
      path: /cron
      expect: {status: 200}
`
}

// source is the probe app at version with contract as its gregale.verify.yaml.
func (j *verifyJourney) source(t *testing.T, version, contract string) string {
	t.Helper()
	dir := probeSource(t, version)
	if err := os.WriteFile(filepath.Join(dir, "gregale.verify.yaml"), []byte(contract), 0o644); err != nil {
		t.Fatalf("write gregale.verify.yaml: %v", err)
	}
	return dir
}

func (j *verifyJourney) passingContractPromotes(t *testing.T) {
	j.tg.ok(t, 5*time.Minute, "env", "set", "--app", j.app, "E2E_VERIFY_TOKEN="+j.token)
	receipt := j.tg.deploy(t, j.app, "--path", j.source(t, "v1", j.verifyContract("v1", "E2E_VERIFY_TOKEN")),
		"--rollout", "immediate")
	report := field(receipt, "hosting_receipt.smoke.verification_contract")
	if report == nil {
		t.Fatalf("the receipt carries no verification report: %s", compact(field(receipt, "hosting_receipt.smoke")))
	}
	if str(report, "status") != "passed" || str(report, "environment") != "production" {
		t.Fatalf("verification report: %s", compact(report))
	}
	checks := checksByName(report)
	for name, want := range map[string]string{"serves": "passed", "authorized": "passed", "cron-delivery": "skipped"} {
		if got := str(checks[name], "status"); got != want {
			t.Errorf("check %s = %s, want %s: %s", name, got, want, compact(checks[name]))
		}
	}
	if raw := compact(report); strings.Contains(raw, j.token) {
		t.Fatalf("the report carries the secret value: %s", raw)
	}
	expectVersion(t, j.base, "v1", time.Minute)
}

func (j *verifyJourney) contractStored(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "release", "check", "get", "--app", j.app).stdout)
	if num(doc, "check_count") != 3 || num(doc, "revision") < 1 || len(str(doc, "contract_sha256")) != 64 {
		t.Fatalf("release check get: %s", compact(doc))
	}
}

func (j *verifyJourney) failingCheckBlocks(t *testing.T) {
	// v2's / says v2, so a contract expecting "never-served" fails its
	// required check.
	report := j.refused(t, j.source(t, "v2", j.verifyContract("never-served", "E2E_VERIFY_TOKEN")))
	if got := str(checksByName(report)["serves"], "code"); got != "verify_assertion_failed" {
		t.Fatalf("serves check code = %q, want verify_assertion_failed: %s", got, compact(report))
	}
}

func (j *verifyJourney) unsetSecretBlocks(t *testing.T) {
	report := j.refused(t, j.source(t, "v3", j.verifyContract("v3", "E2E_VERIFY_UNSET")))
	check := checksByName(report)["authorized"]
	if str(check, "code") != "verify_secret_missing" || !strings.Contains(compact(check), "E2E_VERIFY_UNSET") {
		t.Fatalf("authorized check = %s, want verify_secret_missing naming E2E_VERIFY_UNSET", compact(check))
	}
}

// refused deploys src, requires the deploy to fail on its verification
// contract, and returns the failed deployment's report.
func (j *verifyJourney) refused(t *testing.T, src string) map[string]any {
	t.Helper()
	tg := j.tg
	tg.deploySlots <- struct{}{}
	res := tg.exec(t, tg.token, t.TempDir(), deployLimit+5*time.Minute, "deploy", "--name", j.app,
		"--path", src, "--wait", "--timeout", deployLimit.String(), "--rollout", "immediate",
		"--yes", "--no-require-authn", "--reason", "e2e-journeys run "+tg.runID)
	<-tg.deploySlots
	if res.exit == 0 {
		t.Fatalf("a release whose verification contract fails deployed: %s", tail(string(res.stdout), 800))
	}
	latest := object(t, tg.ok(t, time.Minute, "deployment", "latest", "--app", j.app).stdout)
	if got := str(latest, "hosting_receipt.smoke.error_code"); got != "smoke_verification_contract_failed" &&
		!strings.Contains(compact(latest), "deployment_verification_contract_failed") {
		t.Fatalf("the failed deployment does not name its verification contract (smoke error %q): %s",
			got, tail(compact(latest), 1500))
	}
	report, _ := field(latest, "hosting_receipt.smoke.verification_contract").(map[string]any)
	if report == nil || str(report, "status") != "failed" {
		t.Fatalf("the failed deployment's verification report: %s", compact(field(latest, "hosting_receipt.smoke")))
	}
	return report
}

// checksByName indexes a report's checks.
func checksByName(report any) map[string]map[string]any {
	out := map[string]map[string]any{}
	list, _ := field(report, "checks").([]any)
	for _, c := range list {
		if m, ok := c.(map[string]any); ok {
			out[str(m, "name")] = m
		}
	}
	return out
}
