// adr: 20261029155852
package imaged

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// BaseProvenanceMode is FAAS_IMAGED_BASE_PROVENANCE: what imaged does with a
// platform base whose provenance does not verify (ADR-20261029155852).
type BaseProvenanceMode string

const (
	BaseProvenanceOff     BaseProvenanceMode = "off"
	BaseProvenanceObserve BaseProvenanceMode = "observe"
	BaseProvenanceEnforce BaseProvenanceMode = "enforce"
)

const (
	BaseProvenanceModeEnv = "FAAS_IMAGED_BASE_PROVENANCE"
	CosignBinEnv          = "FAAS_COSIGN_BIN"
	// DefaultCosignBin is where node join stages the release-pinned cosign.
	DefaultCosignBin = "/usr/local/bin/cosign"
	// The trusted root and the frozen legacy list are support files of the
	// signed release, beside the imaged binary.
	BaseProvenanceTrustedRootName = "sigstore-trusted-root.json"
	BaseProvenanceLegacyName      = "base-provenance-legacy.txt"

	// The policy release.yml's gate applies: images.yml on main, signed with
	// a GitHub Actions OIDC token on a GitHub-hosted runner.
	BaseProvenanceIdentity = "https://github.com/poyrazK/faas/.github/workflows/images.yml@refs/heads/main"
	BaseProvenanceIssuer   = "https://token.actions.githubusercontent.com"
	baseProvenanceRunner   = "github-hosted"
)

// Results of one check: the result label of
// imaged_base_provenance_verifications_total.
const (
	BaseProvenanceVerified    = "verified"
	BaseProvenanceCached      = "cached"
	BaseProvenanceLegacy      = "legacy"
	BaseProvenanceMissing     = "missing"
	BaseProvenanceRejected    = "rejected"
	BaseProvenanceUnavailable = "unavailable"
	BaseProvenanceError       = "error"
)

// ErrBaseProvenanceUnverified refuses a base in enforce mode.
var ErrBaseProvenanceUnverified = errors.New("imaged: base provenance unverified")

var (
	errCosignRejected    = errors.New("cosign rejected the bundle")
	errCosignUnavailable = errors.New("cosign is unavailable")
)

// fulcioRunnerEnvironmentOID is Fulcio's runner environment extension
// ("github-hosted" or "self-hosted"). cosign 2.4.1 has no flag for it.
var fulcioRunnerEnvironmentOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 11}

var baseProvenanceDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ParseBaseProvenanceMode reads FAAS_IMAGED_BASE_PROVENANCE; unset is observe.
func ParseBaseProvenanceMode(raw string) (BaseProvenanceMode, error) {
	switch mode := BaseProvenanceMode(strings.TrimSpace(raw)); mode {
	case "":
		return BaseProvenanceObserve, nil
	case BaseProvenanceOff, BaseProvenanceObserve, BaseProvenanceEnforce:
		return mode, nil
	}
	return "", fmt.Errorf("imaged: %s=%q: want off, observe or enforce", BaseProvenanceModeEnv, raw)
}

// LoadBaseProvenanceLegacy reads the frozen pre-attestation digest list. A
// missing file is an empty list (a development build ships none); a line that
// is neither a comment nor a sha256 digest is an error.
func LoadBaseProvenanceLegacy(path string) (map[string]struct{}, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]struct{}{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("imaged: read base provenance legacy list: %w", err)
	}
	legacy := map[string]struct{}{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !baseProvenanceDigestPattern.MatchString(line) {
			return nil, fmt.Errorf("imaged: %s line %d is not a sha256 digest", path, n)
		}
		legacy[line] = struct{}{}
	}
	return legacy, scanner.Err()
}

// BaseProvenanceVerifier checks a platform base digest's SLSA provenance
// before imaged builds the base: the frozen legacy list, then a verdict this
// node already recorded, then the host's cosign run offline on each Sigstore
// bundle the registry holds for the digest.
type BaseProvenanceVerifier struct {
	Mode        BaseProvenanceMode
	CosignBin   string
	TrustedRoot string
	// CacheDir holds one verified verdict per digest; empty records none.
	CacheDir string
	Legacy   map[string]struct{}
	Bundles  oci.SigstoreBundlePuller
	// Cosign runs cosign with args and HOME; nil runs CosignBin as a bounded
	// subprocess. It returns errCosignRejected or errCosignUnavailable.
	Cosign func(ctx context.Context, args []string, home string) error
	Log    *slog.Logger
	Now    func() time.Time

	mu      sync.Mutex
	metrics *prometheus.CounterVec
}

// RegisterMetrics registers imaged_base_provenance_verifications_total.
func (v *BaseProvenanceVerifier) RegisterMetrics(reg prometheus.Registerer) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.metrics = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "imaged_base_provenance_verifications_total",
		Help: "Platform base provenance checks by base repository and result (verified, cached, legacy, missing, rejected, unavailable, error). ADR-20261029155852.",
	}, []string{"base", "result"})
	reg.MustRegister(v.metrics)
}

// Check verifies digest, whose manifest bytes are manifest, from ref's
// repository. It returns the result; in enforce mode it also returns
// ErrBaseProvenanceUnverified unless the digest is verified, cached or legacy.
func (v *BaseProvenanceVerifier) Check(ctx context.Context, ref, digest string, manifest []byte) (string, error) {
	if v == nil || v.Mode == BaseProvenanceOff {
		return "", nil
	}
	result, detail := v.check(ctx, ref, digest, manifest)
	v.record(ref, digest, result, detail)
	switch result {
	case BaseProvenanceVerified, BaseProvenanceCached, BaseProvenanceLegacy:
		return result, nil
	}
	if v.Mode == BaseProvenanceEnforce {
		return result, fmt.Errorf("%w: %s (%s): %s", ErrBaseProvenanceUnverified, ref, digest, result)
	}
	return result, nil
}

func (v *BaseProvenanceVerifier) check(ctx context.Context, ref, digest string, manifest []byte) (string, string) {
	if !baseProvenanceDigestPattern.MatchString(digest) || len(manifest) == 0 || imageDigest(manifest) != digest {
		return BaseProvenanceError, "manifest bytes do not match the digest"
	}
	if _, ok := v.Legacy[digest]; ok {
		return BaseProvenanceLegacy, ""
	}
	if v.cachedVerdict(digest) {
		return BaseProvenanceCached, ""
	}
	root, err := os.ReadFile(v.TrustedRoot)
	if err != nil || v.Bundles == nil || v.CosignBin == "" {
		return BaseProvenanceUnavailable, "no trusted root, cosign or bundle reader"
	}
	bundles, err := v.Bundles.PullSigstoreBundles(ctx, ref, digest, nil)
	if errors.Is(err, oci.ErrSigstoreBundleMissing) {
		return BaseProvenanceMissing, ""
	}
	if err != nil {
		return BaseProvenanceError, err.Error()
	}
	work, err := os.MkdirTemp("", "base-provenance-")
	if err != nil {
		return BaseProvenanceError, err.Error()
	}
	defer func() { _ = os.RemoveAll(work) }()
	manifestPath := filepath.Join(work, "manifest.json")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		return BaseProvenanceError, err.Error()
	}
	detail := ""
	for i, bundle := range bundles {
		bundlePath := filepath.Join(work, fmt.Sprintf("bundle-%d.json", i))
		if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
			return BaseProvenanceError, err.Error()
		}
		err := v.runCosign(ctx, cosignVerifyArgs(bundlePath, v.TrustedRoot, manifestPath), work)
		switch {
		case errors.Is(err, errCosignUnavailable):
			return BaseProvenanceUnavailable, err.Error()
		case errors.Is(err, errCosignRejected):
			detail = err.Error()
			continue
		case err != nil:
			return BaseProvenanceError, err.Error()
		}
		runner, err := bundleRunnerEnvironment(bundle)
		if err != nil || runner != baseProvenanceRunner {
			detail = fmt.Sprintf("signed on runner environment %q", runner)
			continue
		}
		v.writeVerdict(digest, bundle, root)
		return BaseProvenanceVerified, ""
	}
	return BaseProvenanceRejected, detail
}

func cosignVerifyArgs(bundle, trustedRoot, blob string) []string {
	return []string{
		"verify-blob-attestation", "--new-bundle-format", "--offline",
		"--bundle", bundle, "--trusted-root", trustedRoot,
		"--type", "slsaprovenance1",
		"--certificate-identity", BaseProvenanceIdentity,
		"--certificate-oidc-issuer", BaseProvenanceIssuer,
		blob,
	}
}

func (v *BaseProvenanceVerifier) runCosign(ctx context.Context, args []string, home string) error {
	if v.Cosign != nil {
		return v.Cosign(ctx, args, home)
	}
	ctx, cancel := context.WithTimeout(ctx, api.BaseProvenanceVerifyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, v.CosignBin, args...)
	// No proxy or Sigstore variables leak in; --offline needs no network.
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + home}
	cmd.WaitDelay = api.ApplicationStandardRuntimeCleanupTimeout
	stdout := &scanCommandBuffer{limit: api.ApplicationStandardScanMaxErrorBytes}
	stderr := &scanCommandBuffer{limit: api.ApplicationStandardScanMaxErrorBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return fmt.Errorf("cosign timed out after %s", api.BaseProvenanceVerifyTimeout)
	case errors.As(err, &exitErr):
		return fmt.Errorf("%w: %s", errCosignRejected, lastLine(stderr.buffer.String()))
	default:
		return fmt.Errorf("%w: %w", errCosignUnavailable, err)
	}
}

// lastLine keeps cosign's final error line, which names the failed check.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// bundleRunnerEnvironment reads the runner environment extension from the
// bundle's signing certificate (bundle v0.3, or the v0.1/v0.2 chain).
func bundleRunnerEnvironment(bundle []byte) (string, error) {
	var b struct {
		VerificationMaterial struct {
			Certificate *struct {
				RawBytes []byte `json:"rawBytes"`
			} `json:"certificate"`
			X509CertificateChain *struct {
				Certificates []struct {
					RawBytes []byte `json:"rawBytes"`
				} `json:"certificates"`
			} `json:"x509CertificateChain"`
		} `json:"verificationMaterial"`
	}
	if err := json.Unmarshal(bundle, &b); err != nil {
		return "", fmt.Errorf("parse bundle: %w", err)
	}
	var der []byte
	switch vm := b.VerificationMaterial; {
	case vm.Certificate != nil:
		der = vm.Certificate.RawBytes
	case vm.X509CertificateChain != nil && len(vm.X509CertificateChain.Certificates) > 0:
		der = vm.X509CertificateChain.Certificates[0].RawBytes
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return "", fmt.Errorf("parse signing certificate: %w", err)
	}
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(fulcioRunnerEnvironmentOID) {
			continue
		}
		var runner string
		if _, err := asn1.Unmarshal(ext.Value, &runner); err != nil {
			return "", fmt.Errorf("parse runner environment: %w", err)
		}
		return runner, nil
	}
	return "", errors.New("signing certificate has no runner environment")
}

type baseProvenanceVerdict struct {
	Digest            string    `json:"digest"`
	Identity          string    `json:"identity"`
	BundleSHA256      string    `json:"bundle_sha256"`
	TrustedRootSHA256 string    `json:"trusted_root_sha256"`
	VerifiedAt        time.Time `json:"verified_at"`
}

func (v *BaseProvenanceVerifier) verdictPath(digest string) string {
	return filepath.Join(v.CacheDir, "sha256-"+strings.TrimPrefix(digest, "sha256:")+".json")
}

func (v *BaseProvenanceVerifier) cachedVerdict(digest string) bool {
	if v.CacheDir == "" {
		return false
	}
	data, err := os.ReadFile(v.verdictPath(digest))
	if err != nil {
		return false
	}
	var verdict baseProvenanceVerdict
	return json.Unmarshal(data, &verdict) == nil && verdict.Digest == digest && verdict.Identity == BaseProvenanceIdentity
}

// writeVerdict records a verified digest. A failed write only costs a
// repeat verification on the next build.
func (v *BaseProvenanceVerifier) writeVerdict(digest string, bundle, root []byte) {
	if v.CacheDir == "" {
		return
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	body, err := json.Marshal(baseProvenanceVerdict{
		Digest: digest, Identity: BaseProvenanceIdentity,
		BundleSHA256: sha256Hex(bundle), TrustedRootSHA256: sha256Hex(root), VerifiedAt: now().UTC(),
	})
	if err == nil {
		err = writeFileAtomic(v.CacheDir, v.verdictPath(digest), body)
	}
	if err != nil && v.Log != nil {
		v.Log.Warn("imaged: record base provenance verdict", "digest", digest, "err", err)
	}
}

func writeFileAtomic(dir, path string, body []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".verdict-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := errors.Join(tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func imageDigest(b []byte) string { return "sha256:" + sha256Hex(b) }

func (v *BaseProvenanceVerifier) record(ref, digest, result, detail string) {
	v.mu.Lock()
	metrics := v.metrics
	v.mu.Unlock()
	base := baseProvenanceLabel(ref)
	if metrics != nil {
		metrics.WithLabelValues(base, result).Inc()
	}
	if v.Log == nil {
		return
	}
	attrs := []any{"ref", ref, "digest", digest, "base", base, "result", result, "mode", string(v.Mode)}
	if detail != "" {
		attrs = append(attrs, "detail", detail)
	}
	switch result {
	case BaseProvenanceVerified, BaseProvenanceCached, BaseProvenanceLegacy:
		v.Log.Info("imaged: base provenance", attrs...)
	default:
		v.Log.Warn("imaged: base provenance unverified", attrs...)
	}
}

// checkBaseProvenance runs before a base is built from resolved. The subject
// is the linux/amd64 manifest images.yml attests: the selected child of an
// index, or the manifest itself.
func (h *Handler) checkBaseProvenance(ctx context.Context, resolved oci.ImageResolution) error {
	if h.baseProvenance == nil || resolved.Evidence == nil {
		return nil
	}
	manifest := resolved.Evidence.SelectedManifest
	if len(manifest) == 0 {
		manifest = resolved.Evidence.SourceManifest
	}
	_, err := h.baseProvenance.Check(ctx, resolved.Reference, resolved.Digest, manifest)
	return err
}

// baseProvenanceLabel is the repository's last path element, e.g.
// runner-node22, which keeps the metric's cardinality to the base catalog.
func baseProvenanceLabel(ref string) string {
	r, err := oci.ParseReference(ref)
	if err != nil {
		return "unknown"
	}
	return r.Repository[strings.LastIndex(r.Repository, "/")+1:]
}
