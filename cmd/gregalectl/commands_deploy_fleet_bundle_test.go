package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fleetbundle"
	"github.com/onebox-faas/faas/pkg/nodeclaim"
)

func signedTestClaim(name string) nodeclaim.Claim {
	return nodeclaim.Claim{
		APIVersion: nodeclaim.APIVersion,
		Kind:       nodeclaim.Kind,
		Metadata:   nodeclaim.Metadata{Name: name},
		Spec: nodeclaim.Spec{
			SSH: nodeclaim.SSH{
				Host:          "203.0.113.27",
				User:          "root",
				Port:          22,
				HostKeySHA256: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			},
		},
	}
}

func TestValidateFleetBundleManifestRequiresDeclaredComputeNode(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	bundle := &fleetbundle.Bundle{Spec: fleetbundle.Spec{Claims: []nodeclaim.Claim{signedTestClaim("fsn-2")}}}
	if errs, err := validateFleetBundleManifest(bundle, manifestPath); err != nil || errs != nil {
		t.Fatalf("valid compute claim rejected: errs=%v err=%v", errs, err)
	}

	bundle.Spec.Claims = []nodeclaim.Claim{signedTestClaim("fsn-1"), signedTestClaim("fsn-3")}
	errs, err := validateFleetBundleManifest(bundle, manifestPath)
	if err != nil {
		t.Fatalf("validateFleetBundleManifest: %v", err)
	}
	if len(errs) != 2 || !containsFleetBundleError(errs, "requires compute-only") || !containsFleetBundleError(errs, "does not authorize dynamic node") {
		t.Fatalf("manifest membership errors = %v", errs)
	}
}

func TestValidateFleetBundleManifestAllowsPolicyAuthorizedDynamicNode(t *testing.T) {
	manifestPath := dynamicSplitboxJoinManifest(t)
	bundle := &fleetbundle.Bundle{Spec: fleetbundle.Spec{Claims: []nodeclaim.Claim{signedTestClaim("fsn-4")}}}
	if errs, err := validateFleetBundleManifest(bundle, manifestPath); err != nil || errs != nil {
		t.Fatalf("dynamic compute claim rejected: errs=%v err=%v", errs, err)
	}
	bundle.Spec.Claims = []nodeclaim.Claim{signedTestClaim("other-4")}
	if errs, err := validateFleetBundleManifest(bundle, manifestPath); err != nil || !containsFleetBundleError(errs, "name prefix") {
		t.Fatalf("outside-policy claim errors=%v err=%v", errs, err)
	}
}

func TestResolveFleetBundleInputsUsesSignedClaimAndRequiresReplayState(t *testing.T) {
	old := fleetBundleVerifier
	t.Cleanup(func() { fleetBundleVerifier = old })
	bundle := &fleetbundle.Bundle{
		APIVersion: nodeclaim.APIVersion,
		Kind:       fleetbundle.Kind,
		Metadata:   fleetbundle.Metadata{Name: "production", Generation: 1},
		Spec: fleetbundle.Spec{
			IssuedAt:  time.Now().UTC().Add(-time.Minute),
			ExpiresAt: time.Now().UTC().Add(time.Hour),
			Nonce:     "MDEyMzQ1Njc4OWFiY2RlZg",
			Claims:    []nodeclaim.Claim{signedTestClaim("fsn-4")},
		},
	}
	fleetBundleVerifier = func(string, string, string, string, time.Time) (*fleetbundle.Bundle, string, fleetbundle.Errors, error) {
		return bundle, "test-identity", nil, nil
	}
	file := filepath.Join(t.TempDir(), "bundle.yaml")
	if err := os.WriteFile(file, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := deployJoinOptions{FleetBundleFile: file, FleetBundleSignature: file, DryRun: true}
	if err := resolveFleetBundleInputs(&opts); err != nil {
		t.Fatalf("resolveFleetBundleInputs: %v", err)
	}
	if opts.Node != "fsn-4" || opts.SSHHost != "203.0.113.27" || opts.SSHUser != "root" || opts.SSHPort != 22 || opts.SSHHostKeySHA256 == "" {
		t.Fatalf("signed claim was not copied: %#v", opts)
	}
	if !opts.FleetBundleVerified {
		t.Fatal("verified bundle was not retained as the dynamic enrollment authorization")
	}

	opts.DryRun = false
	if err := resolveFleetBundleInputs(&opts); !errors.Is(err, fleetbundle.ErrStateRequired) {
		t.Fatalf("missing replay state error = %v, want ErrStateRequired", err)
	}
	opts.FleetReplayState = "relative-replay-state"
	if err := resolveFleetBundleInputs(&opts); err == nil || !strings.Contains(err.Error(), "absolute durable path") {
		t.Fatalf("relative replay state error = %v", err)
	}
}

func TestCmdDeployFleetBundleCreateRefusesOverwrite(t *testing.T) {
	claimPath := filepath.Join(t.TempDir(), "claim.yaml")
	claimBody := `api_version: gregale.dev/v1alpha1
kind: ComputeNodeClaim
metadata:
  name: fsn-4
spec:
  ssh:
    host: 203.0.113.27
    host_key_sha256: SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
`
	if err := os.WriteFile(claimPath, []byte(claimBody), 0o600); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "fleet-enrollment.yaml")
	if code := cmdDeployFleetBundle([]string{"create", "--claim-file", claimPath, "--generation", "7", "--output", outPath}); code != 0 {
		t.Fatalf("create exit code = %d", code)
	}
	bundle, err := fleetbundle.Load(outPath)
	if err != nil {
		t.Fatalf("load created bundle: %v", err)
	}
	if errs := bundle.Validate(); errs != nil {
		t.Fatalf("created bundle invalid: %v", errs)
	}
	if code := cmdDeployFleetBundle([]string{"create", "--claim-file", claimPath, "--generation", "8", "--output", outPath}); code == 0 {
		t.Fatal("create unexpectedly overwrote an existing bundle")
	}
}

func containsFleetBundleError(errs fleetbundle.Errors, text string) bool {
	for _, err := range errs {
		if strings.Contains(err.Message, text) {
			return true
		}
	}
	return false
}
