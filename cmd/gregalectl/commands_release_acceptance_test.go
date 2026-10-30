package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReleaseAcceptanceCredentialLifecycle(t *testing.T) {
	store := state.NewMemStore()
	oldOpener := releaseAcceptanceStoreOpener
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	t.Cleanup(func() {
		releaseAcceptanceStoreOpener = oldOpener
		osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON
	})
	releaseAcceptanceStoreOpener = func() (state.Store, func(), error) { return store, func() {}, nil }
	var out, stderr bytes.Buffer
	osStdout, osStderr, jsonOutput = &out, &stderr, true

	if code := cmdReleaseAcceptanceMintToken([]string{"--yes", "--reason=" + releaseAcceptanceReason, "--ttl=30m"}); code != 0 {
		t.Fatalf("mint code=%d stderr=%s", code, stderr.String())
	}
	var credential releaseAcceptanceCredential
	if err := json.Unmarshal(out.Bytes(), &credential); err != nil {
		t.Fatal(err)
	}
	if credential.KeyID == "" || credential.AccountID == "" || credential.Token == "" || time.Until(credential.ExpiresAt) <= 0 {
		t.Fatalf("credential = %+v", credential)
	}
	account, key, err := store.AuthenticateKey(context.Background(), api.HashAPIKey(credential.Token))
	if err != nil || account.ID != credential.AccountID || key.ID != credential.KeyID {
		t.Fatalf("AuthenticateKey = account=%s key=%s err=%v", account.ID, key.ID, err)
	}

	out.Reset()
	if code := cmdReleaseAcceptanceRevokeToken([]string{"--key-id", credential.KeyID, "--yes", "--reason=" + releaseAcceptanceReason}); code != 0 {
		t.Fatalf("revoke code=%d stderr=%s", code, stderr.String())
	}
	if _, _, err := store.AuthenticateKey(context.Background(), api.HashAPIKey(credential.Token)); !errors.Is(err, state.ErrAPIKeyRevoked) {
		t.Fatalf("post-revoke auth error = %v, want ErrAPIKeyRevoked", err)
	}
}

func TestReleaseAcceptanceRequiresExplicitMutationAcknowledgement(t *testing.T) {
	if code := cmdReleaseAcceptanceMintToken(nil); code != 2 {
		t.Fatalf("mint without acknowledgement code=%d, want 2", code)
	}
	if code := cmdReleaseAcceptanceRevokeToken([]string{"--key-id", "key"}); code != 2 {
		t.Fatalf("revoke without acknowledgement code=%d, want 2", code)
	}
}

func TestReleaseAcceptanceVerifyPlacementRequiresEveryNodeAndBothFleetShapes(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := ensureReleaseAcceptanceAccount(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.UpsertComputeNode(ctx, state.ComputeNode{ID: "node-b", Name: "node-b", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	local, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	// App ownership is assigned by a distributed claim race, and a live
	// instance may spill away from that owner. Require actual instance
	// placement, not just the app ownership row.
	slugs := []string{"app-a", "fn-a", "app-b"}
	fixtures := []state.App{
		{AccountID: account.ID, Slug: slugs[0], Type: state.AppTypeApp, NodeID: local.ID},
		{AccountID: account.ID, Slug: slugs[1], Type: state.AppTypeFunction, NodeID: local.ID},
		{AccountID: account.ID, Slug: slugs[2], Type: state.AppTypeApp, NodeID: node.ID},
	}
	var lastAppID, lastDeploymentID string
	for i, fixture := range fixtures {
		app, err := store.CreateApp(ctx, fixture)
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:acceptance",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		// All three initially ran on local even though app-b is owned
		// by node-b; the first report must reject false owner coverage.
		if _, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 128, local.ID, ""); err != nil {
			t.Fatal(err)
		}
		if i == len(fixtures)-1 {
			lastAppID, lastDeploymentID = app.ID, deployment.ID
		}
	}

	oldOpener := releaseAcceptanceStoreOpener
	oldOut, oldErr := osStdout, osStderr
	t.Cleanup(func() {
		releaseAcceptanceStoreOpener = oldOpener
		osStdout, osStderr = oldOut, oldErr
	})
	releaseAcceptanceStoreOpener = func() (state.Store, func(), error) { return store, func() {}, nil }
	var out, stderr bytes.Buffer
	osStdout, osStderr = &out, &stderr
	if code := cmdReleaseAcceptanceVerifyPlacement([]string{"--slugs", strings.Join(slugs, ",")}); code != 3 {
		t.Fatalf("owner-only placement accepted: code=%d stderr=%s", code, stderr.String())
	}
	var first releaseAcceptancePlacementReport
	if err := json.Unmarshal(out.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Ready || first.Nodes[1].HasApp {
		t.Fatalf("owner-only report = %+v, want node-b uncovered", first)
	}
	if _, err := store.CreateInstance(ctx, lastAppID, lastDeploymentID, string(state.StateParked), 128, node.ID, ""); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if code := cmdReleaseAcceptanceVerifyPlacement([]string{"--slugs", strings.Join(slugs, ",")}); code != 0 {
		t.Fatalf("verify actual placement code=%d stderr=%s", code, stderr.String())
	}
	var report releaseAcceptancePlacementReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Ready || len(report.Nodes) != 2 {
		t.Fatalf("report = %+v", report)
	}
}

// verify-node is the per-node canary's check: the just-activated node must
// serve an acceptance app AND have built one. rc.301's fsn-2 served every app
// while its builderd failed every build; placement alone would have passed it.
func TestReleaseAcceptanceVerifyNodeRequiresAnInstanceAndABuildOnTheNode(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := ensureReleaseAcceptanceAccount(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	// The registry names a compute-only node fsn-2.faas; the workflow passes
	// the manifest name fsn-2.
	target, err := store.UpsertComputeNode(ctx, state.ComputeNode{ID: "node-fsn-2", Name: "fsn-2.faas", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(slug string) (state.App, state.Deployment, state.Build) {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp})
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
		if err != nil {
			t.Fatal(err)
		}
		build, err := store.CreateBuild(ctx, deployment.ID, state.DeploymentKindTarball, 1024, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateBuildStatus(ctx, build.ID, state.BuildRunning, "", true, false); err != nil {
			t.Fatal(err)
		}
		return app, deployment, build
	}
	succeeded := func(build state.Build, builder string) {
		t.Helper()
		if err := store.UpdateBuildStatus(ctx, build.ID, state.BuildSucceeded, "", false, true); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateBuildProvenance(ctx, state.BuildProvenance{BuildID: build.ID, SourceSHA256: "sha256:src", BuilderNodeID: builder}); err != nil {
			t.Fatal(err)
		}
	}

	oldOpener := releaseAcceptanceStoreOpener
	oldOut, oldErr := osStdout, osStderr
	t.Cleanup(func() {
		releaseAcceptanceStoreOpener = oldOpener
		osStdout, osStderr = oldOut, oldErr
	})
	releaseAcceptanceStoreOpener = func() (state.Store, func(), error) { return store, func() {}, nil }
	var out, stderr bytes.Buffer
	osStdout, osStderr = &out, &stderr
	verify := func(slugs ...string) (int, releaseAcceptanceNodeReport) {
		t.Helper()
		out.Reset()
		stderr.Reset()
		code := cmdReleaseAcceptanceVerifyNode([]string{"--node", "fsn-2", "--slugs", strings.Join(slugs, ",")})
		var report releaseAcceptanceNodeReport
		if code == 0 || code == 3 {
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("code=%d output=%q: %v", code, out.String(), err)
			}
		}
		return code, report
	}

	// Served on the target, built elsewhere: not proven.
	appA, depA, buildA := deploy("canary-a")
	succeeded(buildA, other.Name)
	if _, err := store.CreateInstance(ctx, appA.ID, depA.ID, string(state.StateRunning), 128, target.ID, ""); err != nil {
		t.Fatal(err)
	}
	if code, report := verify("canary-a"); code != 3 || !report.Instance || report.Build || report.Ready {
		t.Fatalf("instance-only node accepted: code=%d report=%+v stderr=%s", code, report, stderr.String())
	}

	// A failed build on the target proves nothing either.
	appB, depB, buildB := deploy("canary-b")
	if err := store.UpdateBuildStatus(ctx, buildB.ID, state.BuildFailed, state.FailureInfra, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstance(ctx, appB.ID, depB.ID, string(state.StateRunning), 128, other.ID, ""); err != nil {
		t.Fatal(err)
	}
	if code, report := verify("canary-a", "canary-b"); code != 3 || report.Build {
		t.Fatalf("failed build counted: code=%d report=%+v", code, report)
	}

	// Built on the target (served elsewhere) completes the proof.
	appC, depC, buildC := deploy("canary-c")
	succeeded(buildC, target.Name)
	if _, err := store.CreateInstance(ctx, appC.ID, depC.ID, string(state.StateParked), 128, other.ID, ""); err != nil {
		t.Fatal(err)
	}
	code, report := verify("canary-a", "canary-c")
	if code != 0 || !report.Ready || report.Node != "fsn-2.faas" || report.NodeID != target.ID {
		t.Fatalf("proven node rejected: code=%d report=%+v stderr=%s", code, report, stderr.String())
	}

	if code, _ := verify("canary-a", "canary-a"); code != 1 {
		t.Fatalf("duplicate slug code=%d, want 1", code)
	}
	if code := cmdReleaseAcceptanceVerifyNode([]string{"--slugs", "canary-a"}); code != 2 {
		t.Fatalf("missing --node code=%d, want 2", code)
	}
}
