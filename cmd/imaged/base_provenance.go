// adr: 20261029155852
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/oci"
)

// newBaseProvenanceVerifier builds the platform base provenance check from
// FAAS_IMAGED_BASE_PROVENANCE. The trusted root and the legacy list are the
// support files of the release this binary came from. An unknown mode or a
// malformed legacy list stops imaged; off returns no verifier.
func newBaseProvenanceVerifier(getenv func(string) string, puller any, storageRoot string, reg prometheus.Registerer, log *slog.Logger) (*imaged.BaseProvenanceVerifier, error) {
	mode, err := imaged.ParseBaseProvenanceMode(getenv(imaged.BaseProvenanceModeEnv))
	if err != nil {
		return nil, err
	}
	if mode == imaged.BaseProvenanceOff {
		log.Warn("imaged: platform base provenance check is off", "env", imaged.BaseProvenanceModeEnv)
		return nil, nil
	}
	legacy, err := imaged.LoadBaseProvenanceLegacy(besideExecutable(imaged.BaseProvenanceLegacyName))
	if err != nil {
		return nil, err
	}
	bundles, _ := puller.(oci.SigstoreBundlePuller)
	cosign := getenv(imaged.CosignBinEnv)
	if cosign == "" {
		cosign = imaged.DefaultCosignBin
	}
	v := &imaged.BaseProvenanceVerifier{
		Mode:        mode,
		CosignBin:   cosign,
		TrustedRoot: besideExecutable(imaged.BaseProvenanceTrustedRootName),
		CacheDir:    filepath.Join(storageRoot, "sigs", "base-provenance"),
		Legacy:      legacy,
		Bundles:     bundles,
		Log:         log,
	}
	v.RegisterMetrics(reg)
	log.Info("imaged: platform base provenance check", "mode", string(mode), "cosign", cosign, "trusted_root", v.TrustedRoot, "legacy_digests", len(legacy))
	return v, nil
}

// besideExecutable names a support file in this binary's release directory.
func besideExecutable(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	return filepath.Join(filepath.Dir(exe), name)
}

type refList []string

func (l *refList) String() string     { return strings.Join(*l, ",") }
func (l *refList) Set(v string) error { *l = append(*l, v); return nil }

type baseProvenanceLine struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest,omitempty"`
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// runBaseProvenanceCommand is `imaged verify-base-provenance`: the same check
// imaged runs before it builds a base, without staging anything. release.yml
// runs it on every digest a release pins; operators run it from the runbook.
// It prints one JSON line per ref and exits 0 only when every ref is verified
// (or listed by --legacy).
func runBaseProvenanceCommand(args []string, stdout, stderr io.Writer) (int, bool) {
	if len(args) == 0 || args[0] != "verify-base-provenance" {
		return 0, false
	}
	fs := flag.NewFlagSet("verify-base-provenance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var refs refList
	fs.Var(&refs, "ref", "digest-pinned platform base reference to verify (repeatable)")
	cosign := fs.String("cosign", envOr(imaged.CosignBinEnv, imaged.DefaultCosignBin), "cosign binary")
	root := fs.String("trusted-root", besideExecutable(imaged.BaseProvenanceTrustedRootName), "Sigstore trusted root")
	legacyPath := fs.String("legacy", "", "frozen pre-attestation digest list; empty requires every ref to verify")
	if err := fs.Parse(args[1:]); err != nil {
		return 2, true
	}
	if len(refs) == 0 || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: imaged verify-base-provenance --ref REF [--ref REF...] [--cosign PATH] [--trusted-root PATH] [--legacy PATH]")
		return 2, true
	}
	legacy := map[string]struct{}{}
	if *legacyPath != "" {
		var err error
		if legacy, err = imaged.LoadBaseProvenanceLegacy(*legacyPath); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 2, true
		}
	}
	client := oci.NewRegistryClient()
	v := &imaged.BaseProvenanceVerifier{
		Mode: imaged.BaseProvenanceObserve, CosignBin: *cosign, TrustedRoot: *root,
		Legacy: legacy, Bundles: client, Log: slog.New(slog.NewTextHandler(stderr, nil)),
	}
	enc := json.NewEncoder(stdout)
	code := 0
	for _, ref := range refs {
		line := verifyBaseProvenanceRef(context.Background(), client, v, ref)
		_ = enc.Encode(line)
		if line.Result != imaged.BaseProvenanceVerified && line.Result != imaged.BaseProvenanceLegacy {
			code = 1
		}
	}
	return code, true
}

func verifyBaseProvenanceRef(ctx context.Context, resolver oci.ImageResolver, v *imaged.BaseProvenanceVerifier, ref string) baseProvenanceLine {
	line := baseProvenanceLine{Ref: ref, Result: imaged.BaseProvenanceError}
	resolved, err := resolver.ResolveImage(ctx, ref, nil)
	if err != nil {
		line.Error = err.Error()
		return line
	}
	if _, err := imagechain.Validate(resolved.Evidence, resolved.SourceDigest, resolved.Digest); err != nil {
		line.Error = err.Error()
		return line
	}
	manifest := resolved.Evidence.SelectedManifest
	if len(manifest) == 0 {
		manifest = resolved.Evidence.SourceManifest
	}
	line.Digest = resolved.Digest
	line.Result, _ = v.Check(ctx, resolved.Reference, resolved.Digest, manifest)
	return line
}
