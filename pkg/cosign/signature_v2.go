package cosign

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// SignatureFormatV2 names a signature bound to the artifact it signs
// (ADR-20261109170000). A version-1 signature is the raw 64-byte ECDSA
// signature over SHA-256 of the artifact's bytes; it says nothing about
// which artifact those bytes belong to, so a signed layer copied over
// another app's key still verifies. Version 2 signs the storage key with
// the digest.
const SignatureFormatV2 = "gregale.artifact-signature.v2"

// maxSignatureV2Bytes bounds a v2 signature document: the format, a storage
// key (storage keys stay well under 1 KiB), a hex digest and a base64
// signature.
const maxSignatureV2Bytes = 4096

// SignatureV2 is the document stored at sigs/<key>.sig for a v2 signature.
type SignatureV2 struct {
	Format string `json:"format"`
	Key    string `json:"key"`
	// Digest is the hex SHA-256 of the artifact's bytes.
	Digest string `json:"digest"`
	// Signature is ECDSA P-256 (r || s) over SHA-256 of PayloadV2.
	Signature []byte `json:"signature"`
}

// PayloadV2 is what a v2 signature signs: the format, the storage key and
// the artifact's hex SHA-256, one per line.
func PayloadV2(key, digestHex string) []byte {
	return []byte(SignatureFormatV2 + "\n" + key + "\n" + digestHex + "\n")
}

// encodeSignatureV2 signs key and digest (the artifact's raw SHA-256) and
// returns the document to store.
func encodeSignatureV2(priv *ecdsa.PrivateKey, key string, digest []byte) ([]byte, error) {
	if key == "" || len(digest) != sha256.Size {
		return nil, errors.New("cosign: v2 signature needs the key and a SHA-256 digest")
	}
	digestHex := hex.EncodeToString(digest)
	sig, err := signRaw(priv, PayloadV2(key, digestHex))
	if err != nil {
		return nil, err
	}
	return json.Marshal(SignatureV2{Format: SignatureFormatV2, Key: key, Digest: digestHex, Signature: sig})
}

// SignV2 signs key and digestHex, the artifact's lowercase hex SHA-256, and
// returns the v2 document to store. signd signs with it
// (ADR-20261109170000 §3).
func SignV2(priv *ecdsa.PrivateKey, key, digestHex string) ([]byte, error) {
	if !ValidDigestHex(digestHex) {
		return nil, errors.New("cosign: v2 signature needs a lowercase hex SHA-256 digest")
	}
	digest, _ := hex.DecodeString(digestHex)
	return encodeSignatureV2(priv, key, digest)
}

// ValidDigestHex reports whether s is a lowercase hex SHA-256.
func ValidDigestHex(s string) bool {
	if len(s) != 2*sha256.Size {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// parseSignatureV2 decodes a v2 document. It reports ok false for anything
// that is not one, a v1 raw signature included.
func parseSignatureV2(blob []byte) (SignatureV2, bool) {
	if len(blob) == 64 || len(blob) > maxSignatureV2Bytes {
		return SignatureV2{}, false
	}
	var doc SignatureV2
	if err := json.Unmarshal(blob, &doc); err != nil || doc.Format != SignatureFormatV2 {
		return SignatureV2{}, false
	}
	return doc, true
}

// verifySignature checks blob, a v1 or v2 signature, for the artifact at
// key whose bytes hash to digest. A v2 signature must name key and digest.
func verifySignature(pub *ecdsa.PublicKey, key string, digest, blob []byte) bool {
	if len(blob) == 64 {
		return verifyDigest(pub, digest, blob)
	}
	doc, ok := parseSignatureV2(blob)
	if !ok || doc.Key != key || doc.Digest != hex.EncodeToString(digest) {
		return false
	}
	payloadDigest := sha256.Sum256(PayloadV2(doc.Key, doc.Digest))
	return verifyDigest(pub, payloadDigest[:], doc.Signature)
}

// checkSignatureShape is CheckPresent's cheap test, without the artifact:
// a 64-byte v1 signature, or a v2 document for key.
func checkSignatureShape(key string, blob []byte) error {
	if len(blob) == 64 {
		return nil
	}
	doc, ok := parseSignatureV2(blob)
	if !ok {
		return fmt.Errorf("signature is %d bytes and not a %s document", len(blob), SignatureFormatV2)
	}
	if doc.Key != key {
		return fmt.Errorf("signature is for %q", doc.Key)
	}
	if len(doc.Signature) != 64 || len(doc.Digest) != 2*sha256.Size {
		return errors.New("signature document is malformed")
	}
	return nil
}
