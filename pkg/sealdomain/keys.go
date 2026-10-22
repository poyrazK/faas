package sealdomain

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// DefaultDir holds every domain identity on a node, mode 0700 root:root.
// Daemons never read it directly: systemd LoadCredential= copies only the
// unit's own domains into its credential directory.
const DefaultDir = "/etc/faas/secrets/domains"

// IdentityPath is the private identity of d under dir (mode 0400).
func IdentityPath(dir string, d Domain) string { return filepath.Join(dir, string(d)+".age") }

// PreviousIdentityPath is the rotation-overlap twin of IdentityPath (ADR-057).
func PreviousIdentityPath(dir string, d Domain) string {
	return IdentityPath(dir, d) + ".previous"
}

// RecipientPath is the public recipient of d under dir (mode 0444).
func RecipientPath(dir string, d Domain) string { return IdentityPath(dir, d) + ".pub" }

// CredentialName is the systemd credential that carries d's identity into
// a reader unit; the recipient credential appends "_recipient".
func CredentialName(d Domain) string {
	return "faas_seal_domain_" + strings.ReplaceAll(string(d), "-", "_")
}

// ErrNoIdentity means none of the given identity files exists.
var ErrNoIdentity = errors.New("sealdomain: no identity loaded")

// LoadReaderIdentities returns what a reader daemon opens envelopes with:
// the domain identity and its previous twin when present, then each legacy
// identity (fleet, host) and its previous twin when present. Legacy
// identities keep rows readable until the domain's re-seal completes; the
// contract step removes them from the unit. Empty paths are skipped, a
// missing file is skipped, and a file that exists but does not parse is an
// error. Duplicates are dropped. At least one identity must load.
func LoadReaderIdentities(domainPath string, legacyPaths ...string) ([]*age.X25519Identity, error) {
	var out []*age.X25519Identity
	seen := map[string]bool{}
	for _, p := range append([]string{domainPath}, legacyPaths...) {
		if p == "" {
			continue
		}
		for _, candidate := range []string{p, p + ".previous", p + "_previous"} {
			id, err := secretbox.LoadHostKey(candidate)
			if errors.Is(err, secretbox.ErrHostKeyNotFound) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("sealdomain: load identity %q: %w", candidate, err)
			}
			if r := id.Recipient().String(); !seen[r] {
				seen[r] = true
				out = append(out, id)
			}
		}
	}
	if len(out) == 0 {
		return nil, ErrNoIdentity
	}
	return out, nil
}

// WriteMode says which recipients a writer seals a domain's values to.
type WriteMode string

const (
	// WriteAuto seals to the domain and the fleet recipient when the
	// domain recipient is staged, and to the fleet recipient otherwise.
	WriteAuto WriteMode = ""
	// WriteFleet keeps sealing to the fleet recipient alone.
	WriteFleet WriteMode = "fleet"
	// WriteDual seals to the domain and the fleet recipient, so readers of
	// the previous release (which hold only the fleet identity) still open
	// every value and a rollback is safe.
	WriteDual WriteMode = "dual"
	// WriteDomain seals to the domain recipient alone. Only after every
	// reader holds the domain identity and the previous release has left
	// the rollback window.
	WriteDomain WriteMode = "domain"
)

// ParseWriteMode validates a mode from configuration.
func ParseWriteMode(s string) (WriteMode, error) {
	switch m := WriteMode(strings.TrimSpace(s)); m {
	case WriteAuto, WriteFleet, WriteDual, WriteDomain:
		return m, nil
	default:
		return "", fmt.Errorf("sealdomain: write mode %q is not one of fleet, dual, domain", s)
	}
}

// Recipients returns the recipients a writer seals to under mode. fleet is
// the shared fleet recipient; domain is nil until the domain is staged.
func Recipients(mode WriteMode, domain, fleet *age.X25519Recipient) ([]*age.X25519Recipient, error) {
	switch mode {
	case WriteDomain:
		if domain == nil {
			return nil, errors.New("sealdomain: write mode domain needs the domain recipient")
		}
		return []*age.X25519Recipient{domain}, nil
	case WriteDual:
		if domain == nil || fleet == nil {
			return nil, errors.New("sealdomain: write mode dual needs the domain and fleet recipients")
		}
		return []*age.X25519Recipient{domain, fleet}, nil
	case WriteFleet:
		if fleet == nil {
			return nil, errors.New("sealdomain: write mode fleet needs the fleet recipient")
		}
		return []*age.X25519Recipient{fleet}, nil
	case WriteAuto:
		if domain != nil && fleet != nil {
			return []*age.X25519Recipient{domain, fleet}, nil
		}
		if fleet != nil {
			return []*age.X25519Recipient{fleet}, nil
		}
		return nil, errors.New("sealdomain: no recipient staged")
	default:
		return nil, fmt.Errorf("sealdomain: unknown write mode %q", mode)
	}
}
