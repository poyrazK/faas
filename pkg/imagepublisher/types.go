// Package imagepublisher verifies keyed registry publisher evidence without
// depending on transport, storage, daemons or state. Both imaged and the private
// evidence store use the same byte/claim verification boundary (ADR-387).
package imagepublisher

import (
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"github.com/onebox-faas/faas/pkg/ociref"
	"strings"
)

var ErrSignatureMissing = errors.New("cosign: image signature missing")
var ErrSignatureInvalid = errors.New("cosign: image signature invalid")

type TrustedPublisher struct {
	Name      string
	PublicKey *ecdsa.PublicKey
}

func digestBytesFromDigest(digest string) ([]byte, error) {
	if err := ociref.ValidateDigest(digest); err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
}
