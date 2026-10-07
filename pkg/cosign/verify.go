// verify.go loads operator-controlled mirrored publisher keys and retains the
// legacy raw P256 digest primitive from ADR-058. The registry deploy path uses
// VerifyImageSignatureAttachments (ADR-435); raw r||s is not the Cosign
// simple-signing wire format. ADR-038's local ext4 signer remains unchanged.

package cosign

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

// DefaultTrustedPublishersDir is the canonical location for the
// per-app cosign trusted-publisher keys. One .pem per publisher,
// filename = signer_name (the apid-side label). Mode 0444 per file
// (the same posture as DefaultSignPubPath).
const DefaultTrustedPublishersDir = "/etc/faas/secrets/trusted-publishers"

// ErrSignatureMissing is returned by VerifyImageSignature when the
// registry has no cosign signature artifact at the well-known
// sha256-<digest>.sig location. Distinct from ErrSignatureInvalid
// because the operator action is different (either onboard a
// publisher if they're expecting a signed deploy, or relax the
// apps.require_signed flag if they aren't).
var ErrSignatureMissing = imagepublisher.ErrSignatureMissing

// ErrSignatureInvalid is returned by VerifyImageSignature when a
// signature exists but doesn't verify against any trusted publisher
// in the supplied list. Distinct from ErrSignatureMissing because
// the operator action is different (the signature is there, but the
// publisher isn't trusted — either the wrong key was used, or the
// trust list needs to be updated).
var ErrSignatureInvalid = imagepublisher.ErrSignatureInvalid

// TrustedPublisher is one entry in the per-app trust list. Name is
// the apid-side label (matches app_trusted_signers.signer_name);
// PublicKey is the parsed ECDSA P-256 SPKI.
type TrustedPublisher = imagepublisher.TrustedPublisher

// TrustedPublishersFromDir reads every *.pem in dir and parses each
// as an ECDSA P-256 public key. Missing dir → (nil, nil) so callers
// can treat "no publishers" as "fail-closed at the apid gate" rather
// than a hard error (the typical case for apps with require_signed
// disabled). Permission-violating files → error.
//
// The dir is the operator's deploy-time trust root; apid emits the
// per-app truth via pg_notify('trusted_signer_changed'), but imaged
// reads from disk to avoid a Postgres round-trip on every deploy.
// imaged's startup loads once and refreshes on the notify signal.
//
// Empty directory is NOT an error — it just returns an empty slice
// (the apid handler is responsible for the fail-closed gate).
func TrustedPublishersFromDir(dir string) ([]TrustedPublisher, error) {
	if dir == "" {
		return nil, errors.New("cosign: empty trusted-publishers dir")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Treat missing dir as "no trust list" — a fresh
			// install has the dir absent until apid has onboarded
			// its first publisher. imaged never crashes on this
			// path; the apid pre-flight gate (handlers.go::
			// createDeployment) rejects the deploy with 403
			// deploy_signature_invalid before imaged sees it.
			return nil, nil
		}
		return nil, fmt.Errorf("cosign: read trusted-publishers dir %q: %w", dir, err)
	}
	var out []TrustedPublisher
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".pem") {
			continue
		}
		signer := strings.TrimSuffix(name, ".pem")
		full := filepath.Join(dir, name)
		pub, err := LoadPublicKeyFile(full)
		if err != nil {
			return nil, fmt.Errorf("cosign: load trusted publisher %q: %w", full, err)
		}
		out = append(out, TrustedPublisher{Name: signer, PublicKey: pub})
	}
	return out, nil
}

// ImageSignaturePuller supplies the legacy raw digest-signature primitive.
// Registry deployments use ImageSignatureAttachmentPuller instead. This
// interface prescribes no OCI blob location: a detached signature cannot be
// stored under the image manifest's content digest.
type ImageSignaturePuller interface {
	ResolveDigest(ctx context.Context, ref string) (string, error)
	FetchSignature(ctx context.Context, ref, digest string) ([]byte, error)
}

// VerifyImageSignature resolves a digest and verifies a legacy raw r||s
// digest signature against every trusted
// publisher. Returns the name of the first matching publisher and
// the canonical digest on success. Returns ErrSignatureMissing
// when the registry has no signature, ErrSignatureInvalid when the
// signature exists but no publisher matches.
//
// Pure function — no DB, no globals. The trusted-publisher list is
// supplied by the caller (imaged's in-memory cache, refreshed via
// TrustedPublishersFromDir on pg_notify('trusted_signer_changed')).
func VerifyImageSignature(ctx context.Context, puller ImageSignaturePuller, ref string, publishers []TrustedPublisher) (matchedSigner string, digest string, err error) {
	if puller == nil {
		return "", "", errors.New("cosign: nil ImageSignaturePuller")
	}
	if ref == "" {
		return "", "", errors.New("cosign: empty image ref")
	}
	if len(publishers) == 0 {
		// Fail-closed: no publishers means no signature can
		// possibly match. The apid pre-flight already gates this
		// case (handlers.go::createDeployment returns 403 before
		// imaged runs), but defence-in-depth here keeps imaged
		// honest if it's ever called outside the apid pipeline.
		return "", "", ErrSignatureInvalid
	}
	digest, err = puller.ResolveDigest(ctx, ref)
	if err != nil {
		return "", "", fmt.Errorf("cosign: resolve digest for %q: %w", ref, err)
	}
	sig, err := puller.FetchSignature(ctx, ref, digest)
	if err != nil {
		if errors.Is(err, ErrSignatureMissing) {
			return "", digest, ErrSignatureMissing
		}
		return "", digest, fmt.Errorf("cosign: fetch signature for %q: %w", ref, err)
	}
	// digest is "sha256:<hex>"; we verify against the 32 raw bytes
	// (the verifyDigest contract — signer.go:103-122).
	digestBytes, err := digestBytesFromDigest(digest)
	if err != nil {
		return "", digest, fmt.Errorf("cosign: parse digest %q: %w", digest, err)
	}
	for _, p := range publishers {
		if p.PublicKey == nil {
			continue
		}
		if verifyDigest(p.PublicKey, digestBytes, sig) {
			return p.Name, digest, nil
		}
	}
	return "", digest, ErrSignatureInvalid
}

// digestBytesFromDigest strips the "sha256:" prefix and returns the
// 32 raw bytes. The shape is the same as the canonical cosign
// payload: the verifier signs over the digest bytes directly (no
// re-hash), per signer.go::verifyDigest.
func digestBytesFromDigest(digest string) ([]byte, error) {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) {
		return nil, fmt.Errorf("cosign: want sha256: prefix, got %q", digest)
	}
	hex := digest[len(prefix):]
	if len(hex) != 64 {
		return nil, fmt.Errorf("cosign: sha256 hex must be 64 chars, got %d", len(hex))
	}
	out := make([]byte, 32)
	for i := 0; i < 32; i++ {
		hi, ok := hexByte(hex[2*i])
		if !ok {
			return nil, fmt.Errorf("cosign: invalid hex at byte %d: %q", 2*i, hex[2*i])
		}
		lo, ok := hexByte(hex[2*i+1])
		if !ok {
			return nil, fmt.Errorf("cosign: invalid hex at byte %d: %q", 2*i+1, hex[2*i+1])
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

// hexByte returns the 4-bit value of a hex character and whether the
// character was a valid hex digit. Lower-case only (the canonical
// sha256: digest shape never has upper-case hex).
func hexByte(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	default:
		return 0, false
	}
}

// TrustListFromDir reads every `<app_id>--<name>.pem` file in dir
// and returns the per-app trust list. Filename format is the
// apid-side writer's contract (cmd/apid/trusted_publisher_writer.go):
// the leading 36-char UUID segment is the app_id, the trailing
// segment is the operator-chosen signer_name (DNS-1123-label style,
// matching the DB CHECK app_trusted_signers_name_shape).
//
// Sort order within each app is alphabetical by signer_name so the
// cache is deterministic across refreshes (handy for tests; the
// verify path is independent of order — ECDSA verify is commutative).
//
// Empty dir / missing dir → (nil, nil). Malformed filename
// (missing the `--` separator, app_id not a valid UUID, name
// violating the DNS-1123-label pattern) is skipped with a warning
// shape but currently does not surface to the caller — the apid
// writer is the only producer, so a malformed filename is a bug
// in the writer, not a production data input.
func TrustListFromDir(dir string) (map[string][]TrustedPublisher, error) {
	if dir == "" {
		return nil, errors.New("cosign: empty trusted-publishers dir")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cosign: read trusted-publishers dir %q: %w", dir, err)
	}
	byApp := map[string][]TrustedPublisher{}
	const sep = "--"
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".pem") {
			continue
		}
		stem := strings.TrimSuffix(name, ".pem")
		idx := strings.Index(stem, sep)
		if idx <= 0 || idx == len(stem)-len(sep) {
			// Malformed — skip silently. The apid writer is the
			// only producer and would never emit this shape; the
			// skip is a defence against hand-edited files.
			continue
		}
		appID := stem[:idx]
		signer := stem[idx+len(sep):]
		if _, err := uuid.Parse(appID); err != nil {
			continue
		}
		if !dns1123LabelPattern.MatchString(signer) {
			continue
		}
		full := filepath.Join(dir, name)
		pub, err := LoadPublicKeyFile(full)
		if err != nil {
			return nil, fmt.Errorf("cosign: load trusted publisher %q: %w", full, err)
		}
		byApp[appID] = append(byApp[appID], TrustedPublisher{Name: signer, PublicKey: pub})
	}
	for appID := range byApp {
		sort.Slice(byApp[appID], func(i, j int) bool {
			return byApp[appID][i].Name < byApp[appID][j].Name
		})
	}
	return byApp, nil
}

// dns1123LabelPattern mirrors the DB CHECK
// app_trusted_signers_name_shape (migration 00083). Kept local so
// pkg/cosign doesn't import pkg/state for the regex.
var dns1123LabelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
