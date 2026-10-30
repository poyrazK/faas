// adr: 20261029155852
package imaged

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/oci"
)

const provenanceTestRef = "ghcr.io/poyrazk/runner-node22@sha256:df455b473706287c80fe92a1a5f3514c4b79c554898c6792b13ce47a7af892e7"

type fakeBundlePuller struct {
	bundles [][]byte
	err     error
	calls   int
}

func (f *fakeBundlePuller) PullSigstoreBundles(_ context.Context, ref, digest string, auth *oci.BasicAuth) ([][]byte, error) {
	f.calls++
	if auth != nil || !strings.HasSuffix(ref, "@"+digest) {
		return nil, errors.New("unexpected bundle request")
	}
	return f.bundles, f.err
}

func readProvenanceTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// newProvenanceTestVerifier wires the real attested runner-node22 manifest
// and bundle, a trusted root file, a verdict cache and a fake cosign.
func newProvenanceTestVerifier(t *testing.T, mode BaseProvenanceMode, cosign func(context.Context, []string, string) error) (*BaseProvenanceVerifier, *fakeBundlePuller, []byte, string) {
	t.Helper()
	manifest := readProvenanceTestdata(t, "base_provenance_manifest.json")
	puller := &fakeBundlePuller{bundles: [][]byte{readProvenanceTestdata(t, "base_provenance_bundle.json")}}
	dir := t.TempDir()
	root := filepath.Join(dir, BaseProvenanceTrustedRootName)
	if err := os.WriteFile(root, []byte(`{"mediaType":"application/vnd.dev.sigstore.trustedroot+json;version=0.1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	v := &BaseProvenanceVerifier{
		Mode: mode, CosignBin: "/usr/local/bin/cosign", TrustedRoot: root,
		CacheDir: filepath.Join(dir, "cache"), Legacy: map[string]struct{}{}, Bundles: puller, Cosign: cosign,
		Now: func() time.Time { return time.Date(2026, 10, 29, 18, 0, 0, 0, time.UTC) },
	}
	return v, puller, manifest, imageDigest(manifest)
}

func TestParseBaseProvenanceMode(t *testing.T) {
	for raw, want := range map[string]BaseProvenanceMode{"": BaseProvenanceObserve, " observe ": BaseProvenanceObserve, "off": BaseProvenanceOff, "enforce": BaseProvenanceEnforce} {
		if got, err := ParseBaseProvenanceMode(raw); err != nil || got != want {
			t.Errorf("ParseBaseProvenanceMode(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"on", "Enforce", "warn"} {
		if _, err := ParseBaseProvenanceMode(raw); err == nil {
			t.Errorf("ParseBaseProvenanceMode(%q) accepted", raw)
		}
	}
}

func TestLoadBaseProvenanceLegacy(t *testing.T) {
	dir := t.TempDir()
	if got, err := LoadBaseProvenanceLegacy(filepath.Join(dir, "absent.txt")); err != nil || len(got) != 0 {
		t.Fatalf("missing file = %v, %v; want an empty list", got, err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	good := filepath.Join(dir, "good.txt")
	if err := os.WriteFile(good, []byte("# frozen\n\n"+digest+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadBaseProvenanceLegacy(good); err != nil || len(got) != 1 {
		t.Fatalf("list = %v, %v", got, err)
	}
	bad := filepath.Join(dir, "bad.txt")
	if err := os.WriteFile(bad, []byte(digest+"\nghcr.io/poyrazk/runner-node22@"+digest+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseProvenanceLegacy(bad); err == nil {
		t.Fatal("accepted a line that is not a digest")
	}
}

// The release ships images/base-provenance-legacy.txt as frozen; an edit that
// breaks its format would stop every imaged at startup.
func TestCommittedBaseProvenanceLegacyListParses(t *testing.T) {
	legacy, err := LoadBaseProvenanceLegacy(filepath.Join("..", "..", "images", BaseProvenanceLegacyName))
	if err != nil || len(legacy) != 243 {
		t.Fatalf("legacy list = %d digests, %v; want the 243 frozen through v0.1.18-rc.304", len(legacy), err)
	}
	if _, ok := legacy["sha256:6160ae70836dd41eb1b8ab461676dad8961a7a1627d7c6993a6c51f63bc01948"]; !ok {
		t.Fatal("rc.304's runner-node22 digest is not in the legacy list")
	}
}

func TestBundleRunnerEnvironmentReadsTheRealAttestation(t *testing.T) {
	runner, err := bundleRunnerEnvironment(readProvenanceTestdata(t, "base_provenance_bundle.json"))
	if err != nil || runner != "github-hosted" {
		t.Fatalf("runner = %q, %v", runner, err)
	}
}

func TestBaseProvenanceVerifiesThenCaches(t *testing.T) {
	var calls [][]string
	v, puller, manifest, digest := newProvenanceTestVerifier(t, BaseProvenanceEnforce, func(_ context.Context, args []string, home string) error {
		calls = append(calls, args)
		blob, err := os.ReadFile(args[len(args)-1])
		if err != nil || string(blob) != string(readProvenanceTestdata(t, "base_provenance_manifest.json")) {
			t.Errorf("cosign blob is not the manifest: %v", err)
		}
		if !strings.HasPrefix(args[len(args)-1], home) {
			t.Errorf("blob %s outside cosign's private HOME %s", args[len(args)-1], home)
		}
		return nil
	})
	reg := prometheus.NewRegistry()
	v.RegisterMetrics(reg)

	result, err := v.Check(context.Background(), provenanceTestRef, digest, manifest)
	if err != nil || result != BaseProvenanceVerified {
		t.Fatalf("first check = %q, %v", result, err)
	}
	if len(calls) != 1 {
		t.Fatalf("cosign ran %d times, want 1", len(calls))
	}
	for _, want := range []string{"verify-blob-attestation", "--new-bundle-format", "--offline", "slsaprovenance1", BaseProvenanceIdentity, BaseProvenanceIssuer, v.TrustedRoot} {
		if !slices.Contains(calls[0], want) {
			t.Errorf("cosign args %v lack %q", calls[0], want)
		}
	}
	data, err := os.ReadFile(v.verdictPath(digest))
	if err != nil {
		t.Fatal(err)
	}
	var verdict baseProvenanceVerdict
	if err := json.Unmarshal(data, &verdict); err != nil || verdict.Digest != digest || verdict.Identity != BaseProvenanceIdentity || verdict.TrustedRootSHA256 == "" {
		t.Fatalf("verdict = %+v, %v", verdict, err)
	}

	// The per-release rebuild of the same digest needs neither registry nor cosign.
	result, err = v.Check(context.Background(), provenanceTestRef, digest, manifest)
	if err != nil || result != BaseProvenanceCached || len(calls) != 1 || puller.calls != 1 {
		t.Fatalf("second check = %q, %v (cosign %d, registry %d)", result, err, len(calls), puller.calls)
	}
	for result, want := range map[string]float64{BaseProvenanceVerified: 1, BaseProvenanceCached: 1} {
		if got := testutil.ToFloat64(v.metrics.WithLabelValues("runner-node22", result)); got != want {
			t.Errorf("verifications{result=%s} = %v, want %v", result, got, want)
		}
	}
}

func TestBaseProvenanceResults(t *testing.T) {
	rejected := func(context.Context, []string, string) error { return errCosignRejected }
	for _, tc := range []struct {
		name    string
		cosign  func(context.Context, []string, string) error
		setup   func(v *BaseProvenanceVerifier, puller *fakeBundlePuller, digest string)
		want    string
		cosigns bool
	}{
		{name: "legacy digest", setup: func(v *BaseProvenanceVerifier, _ *fakeBundlePuller, digest string) {
			v.Legacy[digest] = struct{}{}
		}, want: BaseProvenanceLegacy},
		{name: "no bundle", setup: func(_ *BaseProvenanceVerifier, p *fakeBundlePuller, _ string) {
			p.bundles, p.err = nil, oci.ErrSigstoreBundleMissing
		}, want: BaseProvenanceMissing},
		{name: "registry error", setup: func(_ *BaseProvenanceVerifier, p *fakeBundlePuller, _ string) {
			p.bundles, p.err = nil, errors.New("registry returned 502")
		}, want: BaseProvenanceError},
		{name: "cosign refuses every bundle", cosign: rejected, want: BaseProvenanceRejected, cosigns: true},
		{name: "self-hosted runner", setup: func(_ *BaseProvenanceVerifier, p *fakeBundlePuller, _ string) {
			p.bundles = [][]byte{runnerTestBundle(t, "self-hosted")}
		}, want: BaseProvenanceRejected, cosigns: true},
		{name: "no trusted root", setup: func(v *BaseProvenanceVerifier, _ *fakeBundlePuller, _ string) {
			v.TrustedRoot = filepath.Join(t.TempDir(), "absent.json")
		}, want: BaseProvenanceUnavailable},
		{name: "cosign missing", cosign: func(context.Context, []string, string) error { return errCosignUnavailable }, want: BaseProvenanceUnavailable, cosigns: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			cosign := func(ctx context.Context, args []string, home string) error {
				ran = true
				if tc.cosign != nil {
					return tc.cosign(ctx, args, home)
				}
				return nil
			}
			for _, mode := range []BaseProvenanceMode{BaseProvenanceObserve, BaseProvenanceEnforce} {
				ran = false
				v, puller, manifest, digest := newProvenanceTestVerifier(t, mode, cosign)
				if tc.setup != nil {
					tc.setup(v, puller, digest)
				}
				result, err := v.Check(context.Background(), provenanceTestRef, digest, manifest)
				if result != tc.want || ran != tc.cosigns {
					t.Fatalf("%s: result = %q (cosign ran %v), want %q (cosign %v)", mode, result, ran, tc.want, tc.cosigns)
				}
				accepted := tc.want == BaseProvenanceLegacy
				if mode == BaseProvenanceEnforce && !accepted && !errors.Is(err, ErrBaseProvenanceUnverified) {
					t.Fatalf("enforce: err = %v, want ErrBaseProvenanceUnverified", err)
				}
				if (mode == BaseProvenanceObserve || accepted) && err != nil {
					t.Fatalf("%s: err = %v, want none", mode, err)
				}
				if _, statErr := os.Stat(v.verdictPath(digest)); statErr == nil {
					t.Fatalf("%s: recorded a verdict for result %q", mode, result)
				}
			}
		})
	}
}

func TestBaseProvenanceRefusesManifestBytesOfAnotherDigest(t *testing.T) {
	v, puller, manifest, _ := newProvenanceTestVerifier(t, BaseProvenanceEnforce, nil)
	result, err := v.Check(context.Background(), provenanceTestRef, "sha256:"+strings.Repeat("0", 64), manifest)
	if result != BaseProvenanceError || !errors.Is(err, ErrBaseProvenanceUnverified) || puller.calls != 0 {
		t.Fatalf("result = %q, %v, registry calls %d", result, err, puller.calls)
	}
}

func TestBaseProvenanceOffChecksNothing(t *testing.T) {
	v, puller, manifest, digest := newProvenanceTestVerifier(t, BaseProvenanceOff, func(context.Context, []string, string) error {
		t.Fatal("cosign ran with the check off")
		return nil
	})
	if result, err := v.Check(context.Background(), provenanceTestRef, digest, manifest); result != "" || err != nil || puller.calls != 0 {
		t.Fatalf("off = %q, %v, registry calls %d", result, err, puller.calls)
	}
	var nilVerifier *BaseProvenanceVerifier
	if result, err := nilVerifier.Check(context.Background(), provenanceTestRef, digest, manifest); result != "" || err != nil {
		t.Fatalf("nil verifier = %q, %v", result, err)
	}
}

// The subject is the selected linux/amd64 child of an index, the digest
// images.yml attests and release.yml pins.
func TestCheckBaseProvenanceUsesTheSelectedManifest(t *testing.T) {
	var seen string
	v, _, manifest, digest := newProvenanceTestVerifier(t, BaseProvenanceEnforce, func(_ context.Context, args []string, _ string) error {
		blob, err := os.ReadFile(args[len(args)-1])
		seen = string(blob)
		return err
	})
	h := &Handler{baseProvenance: v}
	resolved := oci.ImageResolution{
		Reference: provenanceTestRef, Digest: digest,
		Evidence: &imagechain.Evidence{SourceManifest: []byte(`{"mediaType":"application/vnd.oci.image.index.v1+json"}`), SelectedManifest: manifest},
	}
	if err := h.checkBaseProvenance(context.Background(), resolved); err != nil || seen != string(manifest) {
		t.Fatalf("err = %v; cosign saw the selected manifest: %v", err, seen == string(manifest))
	}
	h.baseProvenance.Mode = BaseProvenanceEnforce
	h.baseProvenance.CacheDir = ""
	h.baseProvenance.Cosign = func(context.Context, []string, string) error { return errCosignRejected }
	if err := h.checkBaseProvenance(context.Background(), resolved); !errors.Is(err, ErrBaseProvenanceUnverified) {
		t.Fatalf("enforce refusal = %v", err)
	}
	if err := (&Handler{}).checkBaseProvenance(context.Background(), resolved); err != nil {
		t.Fatalf("unwired handler = %v", err)
	}
}

func TestRunCosignSubprocess(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "cosign")
	body := "#!/bin/sh\n[ -z \"$FAAS_PROVENANCE_LEAK\" ] || exit 7\n[ \"$1\" = ok ] && { echo \"$HOME\" > \"$HOME/home\"; exit 0; }\necho 'Error: expected SAN value' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_PROVENANCE_LEAK", "inherited")
	v := &BaseProvenanceVerifier{CosignBin: script}
	home := t.TempDir()
	if err := v.runCosign(context.Background(), []string{"ok"}, home); err != nil {
		t.Fatalf("ok run = %v (the parent environment leaked if this is exit 7)", err)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "home")); strings.TrimSpace(string(got)) != home {
		t.Fatalf("cosign HOME = %q, want %q", got, home)
	}
	if err := v.runCosign(context.Background(), []string{"refuse"}, home); !errors.Is(err, errCosignRejected) || !strings.Contains(err.Error(), "expected SAN value") {
		t.Fatalf("refusal = %v", err)
	}
	v.CosignBin = filepath.Join(dir, "absent")
	if err := v.runCosign(context.Background(), []string{"ok"}, home); !errors.Is(err, errCosignUnavailable) {
		t.Fatalf("missing binary = %v", err)
	}
}

// runnerTestBundle is a bundle whose signing certificate carries runner.
func runnerTestBundle(t *testing.T, runner string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	value, err := asn1.Marshal(runner)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sigstore-intermediate"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtraExtensions: []pkix.Extension{{Id: fulcioRunnerEnvironmentOID, Value: value}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := json.Marshal(map[string]any{"verificationMaterial": map[string]any{"certificate": map[string]any{"rawBytes": der}}})
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}
