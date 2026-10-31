package sealdomain

import (
	"bytes"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// ProbeNamespace tags a domain probe so it cannot be replayed as any other
// envelope.
const ProbeNamespace = "seal_domain_probe"

const maxProbeBytes = 256

// ProbeValue is the non-secret plaintext of d's probe.
func ProbeValue(d Domain) []byte { return []byte("gregale-seal-domain-v1:" + string(d)) }

// SealProbe seals d's probe to the domain recipient alone, so only a node
// that holds the domain identity can open it.
func SealProbe(d Domain, recipient *age.X25519Recipient) ([]byte, error) {
	if recipient == nil {
		return nil, errors.New("sealdomain: probe needs the domain recipient")
	}
	return secretbox.SealBytes(recipient, ProbeNamespace, ProbeValue(d), maxProbeBytes)
}

// OpenProbe proves that identities open d's probe and that the probe names d.
func OpenProbe(d Domain, identities []*age.X25519Identity, blob []byte) error {
	ns, plaintext, err := secretbox.OpenBytesMulti(identities, blob)
	if err != nil {
		return fmt.Errorf("sealdomain: open %s probe: %w", d, err)
	}
	if ns != ProbeNamespace || !bytes.Equal(plaintext, ProbeValue(d)) {
		return fmt.Errorf("sealdomain: %s probe has the wrong namespace or value", d)
	}
	return nil
}
