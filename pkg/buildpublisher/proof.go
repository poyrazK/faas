package buildpublisher

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type Proof struct {
	PublisherName      string `json:"publisher_name"`
	PublisherKeySHA256 string `json:"publisher_key_sha256"`
	PayloadDigest      string `json:"payload_digest"`
	SignatureDigest    string `json:"signature_digest"`
	Payload            []byte `json:"-"`
	Signature          []byte `json:"-"`
}

type Signer interface {
	Sign(context.Context, Claims) (Proof, error)
}

func Sign(ctx context.Context, claims Claims, name string, key *ecdsa.PrivateKey) (Proof, error) {
	if err := ctx.Err(); err != nil {
		return Proof{}, err
	}
	payload, err := Payload(claims)
	if err != nil || key == nil || key.D == nil || key.D.Sign() <= 0 || key.Curve != elliptic.P256() || key.D.Cmp(elliptic.P256().Params().N) >= 0 || !boundedLabel(name, false) {
		return Proof{}, ErrInvalid
	}
	fingerprint, err := imagepublisher.PublisherKeySHA256(&key.PublicKey)
	if err != nil {
		return Proof{}, ErrInvalid
	}
	x, y := key.ScalarBaseMult(key.D.Bytes())
	if x.Cmp(key.X) != 0 || y.Cmp(key.Y) != 0 {
		return Proof{}, ErrInvalid
	}
	hash := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, key, hash[:])
	if err != nil {
		return Proof{}, err
	}
	if err := ctx.Err(); err != nil {
		return Proof{}, err
	}
	return Proof{PublisherName: name, PublisherKeySHA256: fingerprint, PayloadDigest: digest(payload), SignatureDigest: digest(signature), Payload: payload, Signature: signature}, nil
}

// Verify authenticates the canonical expected claims against an externally
// approved current key. Evidence cannot supply its own key or extra fields.
func Verify(claims Claims, proof Proof, keyDER []byte) error {
	if err := CheckProof(claims, proof); err != nil {
		return err
	}
	parsed, err := x509.ParsePKIXPublicKey(keyDER)
	if err != nil {
		return ErrInvalid
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return ErrInvalid
	}
	fingerprint, err := imagepublisher.PublisherKeySHA256(key)
	if err != nil || fingerprint != proof.PublisherKeySHA256 {
		return ErrInvalid
	}
	hash := sha256.Sum256(proof.Payload)
	if !ecdsa.VerifyASN1(key, hash[:], proof.Signature) {
		return ErrInvalid
	}
	return nil
}

func CheckProof(claims Claims, proof Proof) error {
	payload, err := Payload(claims)
	if err != nil || !boundedLabel(proof.PublisherName, false) || !validHash(proof.PublisherKeySHA256) || !bytes.Equal(payload, proof.Payload) || len(proof.Signature) == 0 || len(proof.Signature) > api.BuildExportMaxPublicationSignatureBytes || digest(proof.Payload) != proof.PayloadDigest || digest(proof.Signature) != proof.SignatureDigest {
		return ErrInvalid
	}
	return nil
}

func (p Proof) Clone() Proof {
	p.Payload = bytes.Clone(p.Payload)
	p.Signature = bytes.Clone(p.Signature)
	return p
}

func digest(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }
