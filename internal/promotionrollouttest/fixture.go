// Package promotionrollouttest supplies synthetic unit-test approvals. It never
// generates native qualification evidence or authorizes a production rollout.
package promotionrollouttest

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
)

const Revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type Fixture struct {
	Gate                                 *promotionrollout.Gate
	Policy                               promotionrollout.Policy
	Approval                             promotionrollout.Approval
	Private                              ed25519.PrivateKey
	Public                               ed25519.PublicKey
	PolicyPath, ApprovalPath, PublicPath string
	Now                                  time.Time
}

func New(t *testing.T, target promotionrollout.Target) *Fixture {
	t.Helper()
	account, err := uuid.Parse(target.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := uuid.Parse(target.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	target = promotionrollout.Target{AccountID: account.String(), ProjectID: project.String()}
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	f := &Fixture{Private: private, Public: public, PolicyPath: filepath.Join(directory, "policy.json"), ApprovalPath: filepath.Join(directory, "approval.json"), PublicPath: filepath.Join(directory, "trust.pub"), Now: time.Now().UTC().Truncate(time.Second)}
	f.Policy = promotionrollout.Policy{SchemaVersion: 1, ApprovalPath: f.ApprovalPath, Targets: []promotionrollout.Target{target}, Capabilities: promotionrollout.Capabilities()}
	scenarios := map[string]any{}
	for _, name := range promotionrollout.NativeTests() {
		if name != "TestProjectEnvironmentPromotionFixtureMeta" {
			scenarios[name] = map[string]any{"schema_version": 1, "scenario": "synthetic-unit-fixture"}
		}
	}
	report, err := json.Marshal(map[string]any{"schema_version": 1, "source_sha": Revision, "log_sha256": strings.Repeat("b", 64), "scope": "project-environment-promotion-only", "qualified": true, "admission_enabled": false, "tests": promotionrollout.NativeTests(), "scenarios": scenarios, "errors": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	f.Approval = promotionrollout.Approval{SchemaVersion: 1, SourceSHA: Revision, IssuedAt: f.Now.Add(-time.Minute), ExpiresAt: f.Now.Add(time.Hour), Targets: f.Policy.Targets, Capabilities: promotionrollout.Capabilities(), NativeReport: report, Gates: map[string]promotionrollout.GateReceipt{}}
	graph, err := json.Marshal(map[string]any{"schema_version": 1, "source_sha": Revision, "log_sha256": strings.Repeat("c", 64),
		"scope": promotionrollout.ServiceGraphScope, "qualified": true, "admission_enabled": false, "tests": promotionrollout.ServiceGraphTests(),
		"scenarios": map[string]any{promotionrollout.ServiceGraphTests()[1]: map[string]any{"schema_version": 1, "scenario": "synthetic-unit-fixture"}}, "errors": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	f.Approval.CapabilityReports = map[promotionrollout.Capability]json.RawMessage{promotionrollout.ServiceGraph: graph}
	coordinatedScenarios := map[string]any{}
	for _, name := range promotionrollout.CoordinatedTests()[1:] {
		coordinatedScenarios[name] = map[string]any{"schema_version": 1, "scenario": "synthetic-unit-fixture"}
	}
	coordinated, err := json.Marshal(map[string]any{"schema_version": 1, "source_sha": Revision, "log_sha256": strings.Repeat("c", 64), "scope": promotionrollout.CoordinatedScope, "qualified": true, "admission_enabled": false, "tests": promotionrollout.CoordinatedTests(), "scenarios": coordinatedScenarios, "errors": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	f.Approval.CapabilityReports[promotionrollout.Coordinated] = coordinated
	for _, capability := range promotionrollout.Capabilities() {
		for _, name := range promotionrollout.RequiredGates(capability) {
			f.Approval.Gates[name] = promotionrollout.GateReceipt{Outcome: "passed", EvidenceSHA256: strings.Repeat("c", 64)}
		}
	}
	f.WriteApproval(t)
	f.WritePolicy(t)
	if err := os.WriteFile(f.PublicPath, []byte(base64.StdEncoding.EncodeToString(public)), 0600); err != nil {
		t.Fatal(err)
	}
	enabled := map[promotionrollout.Capability]bool{}
	for _, capability := range promotionrollout.Capabilities() {
		enabled[capability] = true
	}
	f.Gate = promotionrollout.New(promotionrollout.Config{PolicyPath: f.PolicyPath, PublicKey: public, SourceSHA: Revision, Enabled: enabled, Now: func() time.Time { return f.Now }})
	return f
}

func (f *Fixture) WritePolicy(t *testing.T) {
	t.Helper()
	raw, err := json.Marshal(f.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.PolicyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *Fixture) WriteApproval(t *testing.T) {
	t.Helper()
	raw, err := promotionrollout.Sign(f.Approval, f.Private, f.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.ApprovalPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
