package secretbox

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"filippo.io/age"
)

// Unseal telemetry (ADR 20261024014500). Every decryption secretbox performs
// is reported to an observer, labelled by the key file that opened it. A
// reader daemon that is made to open secrets in bulk, or that is fed
// envelopes no key of its own opens, then shows up in its metrics.

// UnsealObserver receives one call per decryption. identity is the label of
// the key that opened the envelope (see UnsealIdentityLabel), or
// UnsealIdentityNone when no supplied key did.
type UnsealObserver interface {
	SecretUnsealed(identity string, ok bool)
}

const (
	// UnsealIdentityNone labels a decryption that no supplied key opened.
	UnsealIdentityNone = "none"
	// UnsealIdentityUnknown labels a key that was not loaded from a key file
	// by LoadHostKey, such as one generated in memory.
	UnsealIdentityUnknown = "unknown"
	// UnsealIdentityOther labels a key file whose name does not reduce to a
	// short lowercase label, which keeps the label set small.
	UnsealIdentityOther = "other"
)

var (
	unsealMu       sync.RWMutex
	unsealObserver UnsealObserver
	// identityLabels maps a loaded key's recipient to its label. Keys are
	// loaded at start and on rotation, so it stays as small as the set of
	// key files a daemon reads.
	identityLabels = map[string]string{}
)

// SetUnsealObserver installs the observer for this process; nil removes it.
// wire.RegisterDefaultOps installs the daemon's metrics.
func SetUnsealObserver(o UnsealObserver) {
	unsealMu.Lock()
	unsealObserver = o
	unsealMu.Unlock()
}

func currentUnsealObserver() UnsealObserver {
	unsealMu.RLock()
	defer unsealMu.RUnlock()
	return unsealObserver
}

var unsealLabelPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// UnsealIdentityLabel reduces a key file path to the label its decryptions
// are counted under: the domain name for a seal-domain key, "fleet" or
// "host" for those keys, whether the file is read directly or through a
// systemd credential, and whether it is the current key or the previous
// one kept for a rotation.
func UnsealIdentityLabel(path string) string {
	name := filepath.Base(path)
	for _, suffix := range []string{".previous", "_previous"} {
		name = strings.TrimSuffix(name, suffix)
	}
	name = strings.TrimSuffix(name, ".age")
	name = strings.TrimPrefix(name, "faas_seal_domain_")
	name = strings.TrimSuffix(strings.TrimPrefix(name, "faas_"), "_age_identity")
	if !unsealLabelPattern.MatchString(name) {
		return UnsealIdentityOther
	}
	return name
}

// labelIdentity records the label for a key loaded from path.
func labelIdentity(path string, id *age.X25519Identity) {
	label := UnsealIdentityLabel(path)
	unsealMu.Lock()
	identityLabels[id.Recipient().String()] = label
	unsealMu.Unlock()
}

func unsealLabel(id *age.X25519Identity) string {
	if id == nil {
		return UnsealIdentityNone
	}
	unsealMu.RLock()
	label, ok := identityLabels[id.Recipient().String()]
	unsealMu.RUnlock()
	if !ok {
		return UnsealIdentityUnknown
	}
	return label
}

// observedIdentity notes which identity unwrapped the file key.
type observedIdentity struct {
	*age.X25519Identity
	opened **age.X25519Identity
}

func (o observedIdentity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	key, err := o.X25519Identity.Unwrap(stanzas)
	if err == nil {
		*o.opened = o.X25519Identity
	}
	return key, err
}

// decryptAll decrypts blob under any of identities (none nil) and reports
// the outcome to the unseal observer, if one is installed.
func decryptAll(identities []*age.X25519Identity, blob []byte) ([]byte, error) {
	observer := currentUnsealObserver()
	var opened *age.X25519Identity
	asInterface := make([]age.Identity, len(identities))
	for i, id := range identities {
		if observer == nil {
			asInterface[i] = id
			continue
		}
		asInterface[i] = observedIdentity{X25519Identity: id, opened: &opened}
	}
	raw, err := readDecrypted(blob, asInterface)
	if observer != nil {
		observer.SecretUnsealed(unsealLabel(opened), err == nil)
	}
	return raw, err
}

func readDecrypted(blob []byte, identities []age.Identity) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(blob), identities...)
	if err != nil {
		return nil, fmt.Errorf("secretbox: open age reader: %w", err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("secretbox: read plaintext: %w", err)
	}
	return raw, nil
}
