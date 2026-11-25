package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRequalificationPlanRejectsDuplicateFields(t *testing.T) {
	for _, body := range []string{`{"candidate":{"app":"a","app":"b"}}`, `{"version":1,"version":2}`} {
		if err := requalifyUniqueJSON(json.NewDecoder(bytes.NewBufferString(body)), 0); err == nil {
			t.Fatal("accepted duplicate plan fields")
		}
	}
	if err := requalifyUniqueJSON(json.NewDecoder(bytes.NewBufferString(`{"candidate":{"app":"a"}}`)), 0); err != nil {
		t.Fatal(err)
	}
}

func requalificationFixture(t *testing.T) (dataAPIConnectReceipt, *connectRemoteFixture, dataAPIRequalificationPlan, []byte, string) {
	t.Helper()
	old, remote := connectReceiptFixture(t)
	remote.app.URL = "https://notes.example"
	if err := dataAPIConnectSnapshot(context.Background(), remote, &old, true); err != nil {
		t.Fatal(err)
	}
	old.Stage = "verified"
	old.Fingerprint = strings.Repeat("b", 64)
	old.TypesHash = strings.Repeat("c", 64)
	remote.review.LatestDeploymentID = "deployment-2"
	remote.review.ServingDeploymentIDs = []string{"deployment-2"}
	remote.review.ConfigurationHash = strings.Repeat("d", 64)
	candidate, endpoint, err := observeRequalificationCandidate(context.Background(), remote, old)
	if err != nil {
		t.Fatal(err)
	}
	content := "// Schema fingerprint: " + strings.Repeat("e", 64) + "\nexport type X = string\n"
	collection := []byte("reviewed checks")
	candidate.Stage = "deployed"
	candidate.Fingerprint = strings.Repeat("e", 64)
	candidate.TypesHash = dataAPIConnectHash([]byte(content))
	candidate.CollectionHash = dataAPIConnectHash(collection)
	candidate.Output = filepath.Join(t.TempDir(), "candidate.ts")
	plan := dataAPIRequalificationPlan{Version: 1, PreviousReceiptHash: requalifyReceiptHash(old), PreviousDeployment: old.DeploymentID, PreviousFingerprint: old.Fingerprint, PreviousConfigurationHash: old.ConfigurationHash, PreviousTypesHash: old.TypesHash, PreviousCollectionHash: old.CollectionHash, IdentityMappingHash: strings.Repeat("f", 64), Candidate: candidate, Endpoint: endpoint, NewReceipt: filepath.Join(t.TempDir(), "new-receipt.json")}
	return old, remote, plan, collection, content
}

func TestRequalificationObservesNewDeploymentButRefusesReplacedResources(t *testing.T) {
	old, remote, plan, _, _ := requalificationFixture(t)
	if plan.Candidate.DeploymentID == old.DeploymentID || plan.Candidate.ConfigurationHash == old.ConfigurationHash {
		t.Fatal("new candidate was not observed")
	}
	for _, mutate := range []func(*connectRemoteFixture){
		func(f *connectRemoteFixture) { f.app.ID = "other" },
		func(f *connectRemoteFixture) { f.db.ID = "other" },
		func(f *connectRemoteFixture) { f.binding.ID = "other" },
		func(f *connectRemoteFixture) { f.binding.CredentialGeneration++ },
		func(f *connectRemoteFixture) {
			f.review.ServingDeploymentIDs = append(f.review.ServingDeploymentIDs, "other")
		},
	} {
		copy := *remote
		mutate(&copy)
		if _, _, err := observeRequalificationCandidate(context.Background(), &copy, old); err == nil {
			t.Fatal("accepted replaced or unstable resources")
		}
	}
}

func TestRequalificationPlanRequiresExactOriginalIdentityAndChecks(t *testing.T) {
	old, _, plan, collection, _ := requalificationFixture(t)
	oldPath := filepath.Join(t.TempDir(), "old.json")
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := validateRequalificationPlan(plan, old, collection, strings.Repeat("f", 64), oldPath, planPath); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*dataAPIRequalificationPlan){
		func(p *dataAPIRequalificationPlan) { p.PreviousReceiptHash = strings.Repeat("0", 64) },
		func(p *dataAPIRequalificationPlan) { p.IdentityMappingHash = strings.Repeat("0", 64) },
		func(p *dataAPIRequalificationPlan) { p.Candidate.BindingID = "other" },
		func(p *dataAPIRequalificationPlan) { p.Candidate.CredentialGeneration++ },
		func(p *dataAPIRequalificationPlan) { p.Candidate.Stage = "verified" },
		func(p *dataAPIRequalificationPlan) { p.Candidate.CollectionHash = strings.Repeat("0", 64) },
		func(p *dataAPIRequalificationPlan) { p.Candidate.Output = old.Output },
		func(p *dataAPIRequalificationPlan) { p.NewReceipt = oldPath },
	} {
		copy := plan
		mutate(&copy)
		if err := validateRequalificationPlan(copy, old, collection, strings.Repeat("f", 64), oldPath, planPath); err == nil {
			t.Fatal("accepted mismatched plan")
		}
	}
}

func TestRequalificationPinsTypesAndRechecksAfterReplay(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*connectRemoteFixture)
		taskChange bool
		replayErr  error
		wantOK     bool
	}{
		{name: "verified", wantOK: true},
		{name: "wrong types", taskChange: true},
		{name: "failed replay", replayErr: errors.New("failed")},
		{name: "deployment changed during replay", mutate: func(f *connectRemoteFixture) {
			f.review.LatestDeploymentID = "deployment-3"
			f.review.ServingDeploymentIDs = []string{"deployment-3"}
		}},
		{name: "endpoint changed during replay", mutate: func(f *connectRemoteFixture) { f.app.URL = "https://other.example" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, remote, plan, _, content := requalificationFixture(t)
			calls := 0
			generated := func(context.Context, string) (api.AppTaskResponse, error) {
				if tc.taskChange {
					return api.AppTaskResponse{DeploymentID: plan.Candidate.DeploymentID, StdoutTail: content + "changed"}, nil
				}
				return api.AppTaskResponse{DeploymentID: plan.Candidate.DeploymentID, StdoutTail: content}, nil
			}
			replay := func(_ context.Context, endpoint, fingerprint string) error {
				calls++
				if endpoint != plan.Endpoint || fingerprint != plan.Candidate.Fingerprint {
					t.Fatal("replay was not pinned")
				}
				if tc.mutate != nil {
					tc.mutate(remote)
				}
				return tc.replayErr
			}
			_, err := checkRequalification(context.Background(), remote, old, plan, generated, replay)
			if (err == nil) != tc.wantOK {
				t.Fatalf("error=%v", err)
			}
			if tc.taskChange && calls != 0 {
				t.Fatal("replayed mismatched types")
			}
		})
	}
}

func TestRequalificationPlanNeverOverwritesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := writeNewRequalificationPlan(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := writeNewRequalificationPlan(path, []byte("replacement")); err == nil {
		t.Fatal("overwrote plan")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("content=%q err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions=%v err=%v", info, err)
	}
}

func TestRequalificationRejectsIncompleteOrConflictingOptions(t *testing.T) {
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{
		{"notes", "--requalify-plan", "plan.json"},
		{"notes", "--requalify-apply", "plan.json", "--collection", "checks.json", "--identities", "identities.json"},
		{"notes", "--requalify-plan", "plan.json", "--requalify-apply", "plan.json", "--collection", "checks.json", "--identities", "identities.json"},
	} {
		if code := cmdDataAPIConnect(args); code == 0 {
			t.Fatalf("accepted invalid options: %v", args)
		}
	}
}

func TestRequalificationIgnoresNewerNonServingDeployment(t *testing.T) {
	old, remote, plan, _, _ := requalificationFixture(t)
	remote.review.LatestDeploymentID = "newer-failed"
	candidate, _, err := observeRequalificationCandidate(context.Background(), remote, old)
	if err != nil || candidate.DeploymentID != plan.Candidate.DeploymentID {
		t.Fatalf("candidate = %+v, %v", candidate, err)
	}
}
