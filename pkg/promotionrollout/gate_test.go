package promotionrollout_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/internal/promotionrollouttest"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
)

func target() promotionrollout.Target {
	return promotionrollout.Target{AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
}

func unchecked(t *testing.T, approval promotionrollout.Approval, key ed25519.PrivateKey) []byte {
	t.Helper()
	raw, err := json.Marshal(approval)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(key.Public().(ed25519.PublicKey))
	envelope, err := json.Marshal(promotionrollout.Envelope{SchemaVersion: 1, KeyID: hex.EncodeToString(hash[:]), Payload: base64.StdEncoding.EncodeToString(raw), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("gregale.promotion-rollout.v1\n"), raw...)))})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestAdmissionRequiresRevisionScopeCapabilityAndEvidence(t *testing.T) {
	for _, name := range []string{"valid", "compact_lookup", "compact_claim", "paused", "unsigned_scope_widening", "other_account", "other_project", "disabled_capability", "unapproved_capability", "wrong_revision", "expired", "future", "excess_lifetime", "missing_native_partition", "missing_publication_partition", "missing_publication_receipt", "failed_native", "missing_supplemental", "failed_supplemental", "tampered", "untrusted", "missing_policy", "bad_policy", "missing_paused", "case_alias", "null_paused", "missing_approval", "shared_write", "symlink", "oversized"} {
		t.Run(name, func(t *testing.T) {
			selected := target()
			f := promotionrollouttest.New(t, selected)
			switch name {
			case "compact_lookup":
				selected.AccountID = strings.ReplaceAll(selected.AccountID, "-", "")
				selected.ProjectID = strings.ReplaceAll(selected.ProjectID, "-", "")
			case "compact_claim":
				f.Approval.Targets = []promotionrollout.Target{{AccountID: strings.ReplaceAll(selected.AccountID, "-", ""), ProjectID: selected.ProjectID}}
			case "paused":
				f.Policy.Paused = true
				f.WritePolicy(t)
			case "unsigned_scope_widening":
				selected = target()
				f.Policy.Targets = []promotionrollout.Target{selected}
				f.WritePolicy(t)
			case "other_account":
				selected.AccountID = uuid.NewString()
			case "other_project":
				selected.ProjectID = uuid.NewString()
			case "disabled_capability":
				f.Policy.Capabilities = []promotionrollout.Capability{promotionrollout.Health}
				f.WritePolicy(t)
			case "unapproved_capability":
				f.Approval.Capabilities = []promotionrollout.Capability{promotionrollout.Health}
			case "wrong_revision":
				f.Approval.SourceSHA = strings.Repeat("d", 40)
			case "expired":
				f.Now = f.Approval.ExpiresAt
			case "future":
				f.Approval.IssuedAt = f.Now.Add(time.Second)
			case "excess_lifetime":
				f.Approval.ExpiresAt = f.Approval.IssuedAt.Add(api.PromotionRolloutApprovalMaxTTL + time.Second)
			case "missing_native_partition":
				var report map[string]any
				_ = json.Unmarshal(f.Approval.NativeReport, &report)
				report["tests"] = promotionrollout.NativeTests()[:6]
				f.Approval.NativeReport, _ = json.Marshal(report)
			case "failed_native":
				f.Approval.NativeReport = []byte(strings.Replace(string(f.Approval.NativeReport), `"qualified":true`, `"qualified":false`, 1))
			case "missing_publication_partition":
				var report map[string]any
				_ = json.Unmarshal(f.Approval.NativeReport, &report)
				report["tests"] = promotionrollout.NativeTests()[:len(promotionrollout.NativeTests())-1]
				delete(report["scenarios"].(map[string]any), "TestProjectEnvironmentPromotionPublicationMetal")
				f.Approval.NativeReport, _ = json.Marshal(report)
			case "missing_publication_receipt":
				delete(f.Approval.Gates, "publication-crash-boundaries")
			case "missing_supplemental":
				delete(f.Approval.Gates, "migration-resolve")
			case "failed_supplemental":
				f.Approval.Gates["migration-resolve"] = promotionrollout.GateReceipt{Outcome: "failed", EvidenceSHA256: strings.Repeat("d", 64)}
			case "missing_policy":
				_ = os.Remove(f.PolicyPath)
			case "bad_policy":
				_ = os.WriteFile(f.PolicyPath, []byte("{"), 0600)
			case "missing_paused", "case_alias", "null_paused":
				raw, _ := json.Marshal(f.Policy)
				replacement := ""
				old := `"paused":false,`
				if name == "case_alias" {
					replacement = `"Paused":false,`
				}
				if name == "null_paused" {
					replacement = `"paused":null,`
				}
				_ = os.WriteFile(f.PolicyPath, []byte(strings.Replace(string(raw), old, replacement, 1)), 0600)
			case "missing_approval":
				_ = os.Remove(f.ApprovalPath)
			case "shared_write":
				_ = os.Chmod(f.PolicyPath, 0666)
			case "symlink":
				_ = os.Rename(f.PolicyPath, f.PolicyPath+".real")
				_ = os.Symlink(f.PolicyPath+".real", f.PolicyPath)
			case "oversized":
				_ = os.WriteFile(f.PolicyPath, []byte(strings.Repeat(" ", api.PromotionRolloutPolicyMaxBytes+1)), 0600)
			}
			if strings.Contains("compact_claim unapproved_capability wrong_revision future excess_lifetime missing_native_partition missing_publication_partition missing_publication_receipt failed_native missing_supplemental failed_supplemental", name) {
				if err := os.WriteFile(f.ApprovalPath, unchecked(t, f.Approval, f.Private), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if name == "tampered" {
				raw, _ := os.ReadFile(f.ApprovalPath)
				raw = []byte(strings.Replace(string(raw), `"key_id":"`, `"key_id":"0`, 1))
				_ = os.WriteFile(f.ApprovalPath, raw, 0600)
			}
			if name == "untrusted" {
				_, key, _ := ed25519.GenerateKey(nil)
				_ = os.WriteFile(f.ApprovalPath, unchecked(t, f.Approval, key), 0600)
			}
			if got := f.Gate.Check(selected, promotionrollout.Migrations); got != (name == "valid" || name == "compact_lookup") {
				t.Fatalf("allowed=%v readiness=%+v", got, f.Gate.Readiness(selected))
			}
		})
	}
}

func TestPauseAndRemovalNeverUseCachedApproval(t *testing.T) {
	selected := target()
	f := promotionrollouttest.New(t, selected)
	if !f.Gate.Check(selected, promotionrollout.Health) {
		t.Fatal("fixture rejected")
	}
	f.Policy.Paused = true
	f.WritePolicy(t)
	if f.Gate.Check(selected, promotionrollout.Health) {
		t.Fatal("pause did not apply")
	}
	f.Policy.Paused = false
	f.WritePolicy(t)
	if !f.Gate.Check(selected, promotionrollout.Health) {
		t.Fatal("approved cohort did not reopen")
	}
	if err := os.Remove(f.ApprovalPath); err != nil {
		t.Fatal(err)
	}
	if f.Gate.Check(selected, promotionrollout.Health) {
		t.Fatal("cached an unavailable approval")
	}
}

func TestApprovedAccountAndProjectMustBeOnePair(t *testing.T) {
	first, second := target(), target()
	f := promotionrollouttest.New(t, first)
	f.Approval.Targets = append(f.Approval.Targets, second)
	mixed := promotionrollout.Target{AccountID: first.AccountID, ProjectID: second.ProjectID}
	f.Policy.Targets = append(f.Policy.Targets, second, mixed)
	f.WriteApproval(t)
	f.WritePolicy(t)
	if !f.Gate.Check(first, promotionrollout.Health) || !f.Gate.Check(second, promotionrollout.Health) || f.Gate.Check(mixed, promotionrollout.Health) {
		t.Fatal("combined independently approved accounts/projects into an unapproved target")
	}
}

func TestStrictJSONAndSigning(t *testing.T) {
	f := promotionrollouttest.New(t, target())
	for _, raw := range []string{`null`, `{}`, `{"schema_version":1,"schema_version":1}`, `{"schema_version":1} {}`, `{"Schema_version":1}`, `{"schema_version":null}`} {
		var policy promotionrollout.Policy
		if promotionrollout.Decode([]byte(raw), &policy) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	delete(f.Approval.Gates, "migration-resolve")
	if _, err := promotionrollout.Sign(f.Approval, f.Private, f.Now); err == nil {
		t.Fatal("signed incomplete runbook evidence")
	}
	var nilGate *promotionrollout.Gate
	if nilGate.Check(target(), promotionrollout.Health) {
		t.Fatal("nil gate opened admission")
	}
}

func TestEnvironmentFailsClosedOutsideQualificationHost(t *testing.T) {
	f := promotionrollouttest.New(t, target())
	env := map[string]string{promotionrollout.PolicyEnv: f.PolicyPath, promotionrollout.TrustKeyEnv: f.PublicPath, "FAAS_PROMOTION_HEALTH_ENABLED": "1", "FAAS_PROMOTION_MIGRATIONS_ENABLED": "1"}
	get := func(key string) string { return env[key] }
	g := promotionrollout.FromEnv(get, promotionrollouttest.Revision)
	if !g.Check(f.Policy.Targets[0], promotionrollout.Migrations) {
		t.Fatal(g.Readiness(f.Policy.Targets[0]))
	}
	for _, revision := range []string{"unknown", "aaaaaaa", ""} {
		if promotionrollout.FromEnv(get, revision).Check(f.Policy.Targets[0], promotionrollout.Health) {
			t.Fatal("accepted nonexact revision")
		}
	}
	env[promotionrollout.QualificationEnv] = "1"
	env["DATABASE_URL"] = "postgres:///production?host=/run/postgresql"
	if promotionrollout.FromEnv(get, promotionrollouttest.Revision).Check(f.Policy.Targets[0], promotionrollout.Health) {
		t.Fatal("qualification flag bypassed host/database isolation")
	}
}

func TestConcurrentPolicyReplacementUsesOneSnapshot(t *testing.T) {
	selected := target()
	f := promotionrollouttest.New(t, selected)
	var group sync.WaitGroup
	errors := make(chan error, 1)
	group.Go(func() {
		for index := range 40 {
			policy := f.Policy
			policy.Paused = index%2 != 0
			raw, err := json.Marshal(policy)
			if err == nil {
				err = os.WriteFile(f.PolicyPath+".next", raw, 0600)
			}
			if err == nil {
				err = os.Rename(f.PolicyPath+".next", f.PolicyPath)
			}
			if err != nil {
				errors <- err
				return
			}
		}
	})
	consistent := make(chan bool, 3)
	for range 3 {
		group.Go(func() {
			for range 60 {
				report := f.Gate.Readiness(selected)
				for _, capability := range report.Capabilities {
					if capability.Ready != report.Capabilities[0].Ready {
						consistent <- false
						return
					}
				}
			}
			consistent <- true
		})
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	for range 3 {
		if !<-consistent {
			t.Fatal("one readiness result mixed policy snapshots")
		}
	}
}
