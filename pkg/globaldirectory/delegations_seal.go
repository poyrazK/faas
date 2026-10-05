package globaldirectory

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const delegationNamespace = "global-release-delegation"

type DelegationKeys struct{ Identities []*age.X25519Identity }

// The authenticated payload excludes mutable delivery state and server timestamps.
type delegationPayload struct {
	ID, GlobalAppID, Slug, APIBase, Token string
	OwnerID                               string `json:"AccountID"` // ADR-608 ciphertext compatibility.
	Binding                               Binding
	ExpiresAt                             time.Time
}

func (k DelegationKeys) Available() bool {
	return len(k.Identities) > 0 && k.Identities[0] != nil
}
func (k DelegationKeys) Mint(app App, t Target) (Delegation, error) {
	token, hash, err := api.GenerateRegionalReleaseToken()
	if err != nil || !k.Available() {
		return Delegation{}, ErrDelegationSealed
	}
	g := Delegation{ID: uuid.NewString(), GlobalAppID: app.ID, OwnerID: app.OwnerID, Slug: app.Slug,
		Binding: t.Binding, APIBase: t.APIBase, Hash: hash, ExpiresAt: time.Now().UTC().Add(api.GlobalDelegationLifetime).Truncate(time.Microsecond), Status: DelegationPending}
	g.Sealed, err = k.Seal(g, token)
	return g, err
}
func payload(g Delegation, token string) delegationPayload {
	return delegationPayload{ID: g.ID, GlobalAppID: g.GlobalAppID, OwnerID: g.OwnerID, Slug: g.Slug,
		APIBase: g.APIBase, Token: token, Binding: g.Binding, ExpiresAt: g.ExpiresAt}
}
func (k DelegationKeys) Seal(g Delegation, token string) ([]byte, error) {
	if !k.Available() || !g.identityValid() || !api.ValidRegionalReleaseTokenFormat(token) || !bytes.Equal(g.Hash, api.HashAPIKey(token)) {
		return nil, ErrDelegationSealed
	}
	data, err := json.Marshal(payload(g, token))
	if err != nil || len(data) > api.GlobalDelegationPlaintextMaxBytes {
		return nil, ErrDelegationSealed
	}
	blob, err := secretbox.SealBytes(k.Identities[0].Recipient(), delegationNamespace, data, api.GlobalDelegationPlaintextMaxBytes)
	if err != nil || len(blob) > api.GlobalDelegationCiphertextMaxBytes {
		return nil, ErrDelegationSealed
	}
	return blob, nil
}
func (k DelegationKeys) Open(g Delegation) (string, error) {
	if !g.identityValid() || len(g.Sealed) == 0 || len(g.Sealed) > api.GlobalDelegationCiphertextMaxBytes {
		return "", ErrDelegationSealed
	}
	ns, data, err := secretbox.OpenBytesMulti(k.Identities, g.Sealed)
	if err != nil || ns != delegationNamespace || len(data) > api.GlobalDelegationPlaintextMaxBytes {
		return "", ErrDelegationSealed
	}
	var p delegationPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || decoder.Decode(&struct{}{}) != io.EOF || !api.ValidRegionalReleaseTokenFormat(p.Token) {
		return "", ErrDelegationSealed
	}
	expected := payload(g, p.Token)
	if !p.ExpiresAt.Equal(expected.ExpiresAt) {
		return "", ErrDelegationSealed
	}
	p.ExpiresAt = expected.ExpiresAt
	if p != expected || !bytes.Equal(g.Hash, api.HashAPIKey(p.Token)) {
		return "", ErrDelegationSealed
	}
	return p.Token, nil
}
