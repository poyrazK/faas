package datapipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// Source URLs are restricted to the same verified-TLS contract as Commit.
// Identity excludes credentials, so rotation cannot silently change the source.
func ConnectionIdentity(raw string) (string, error) {
	if commit.ValidateConnection(raw) != nil {
		return "", ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrInvalid
	}
	// Never inherit a control-plane or subscriber-process password.
	password, present := u.User.Password()
	if !present || password == "" {
		return "", ErrInvalid
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	h := sha256.Sum256([]byte(u.Hostname() + "\x00" + port + "\x00" + u.Path + "\x00" + u.User.Username()))
	return hex.EncodeToString(h[:]), nil
}
func sealConnection(recipient *age.X25519Recipient, account, id, raw string) ([]byte, string, error) {
	identity, err := ConnectionIdentity(raw)
	if recipient == nil {
		return nil, "", ErrUnavailable
	}
	if err != nil {
		return nil, "", ErrInvalid
	}
	blob, err := secretbox.SealBytes(recipient, "gregale.pipeline.source."+account+"."+id, []byte(raw), api.DataPipelineCredentialMaxBytes)
	if err != nil {
		return nil, "", ErrUnavailable
	}
	return blob, identity, nil
}
func openConnection(identities []*age.X25519Identity, c Connection) (string, error) {
	namespace, raw, err := secretbox.OpenBytesMulti(identities, c.Ciphertext)
	if err != nil || namespace != "gregale.pipeline.source."+c.AccountID+"."+c.ID {
		return "", ErrUnavailable
	}
	identity, err := ConnectionIdentity(string(raw))
	if err != nil || identity != c.Identity {
		return "", ErrUnavailable
	}
	return string(raw), nil
}
