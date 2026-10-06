package globaldirectory

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

type RuntimeConsentService struct {
	Store    Store
	Journal  RuntimeConsentStore
	IssuerID string
	Verifier Verifier
}

// Setup commits pending intent before regional PUT. Retried UUIDs cannot
// replace their original scope or extend expiry, including after a lost reply.
func (s RuntimeConsentService) Setup(ctx context.Context, owner, slug, id, region string, expiry time.Time, key string) (RuntimeConsentRecord, error) {
	if s.Store == nil || s.Journal == nil || !ValidID(s.IssuerID) || !ValidID(id) || !api.ValidAPIKeyFormat(key) || !expiry.Equal(expiry.Truncate(time.Microsecond)) {
		return RuntimeConsentRecord{}, ErrConflict
	}
	a, err := s.Store.AppBySlug(ctx, owner, slug)
	if err != nil {
		return RuntimeConsentRecord{}, err
	}
	t, err := Resolve(ctx, s.Store, owner, slug, region)
	if err != nil {
		return RuntimeConsentRecord{}, err
	}
	c := runtimequota.Consent{ID: id, IssuerID: s.IssuerID, GlobalAppID: a.ID, OwnerID: runtimeConsentOwner(owner), Binding: runtimeConsentBinding(t), ExpiresAtMicros: expiry.UnixMicro()}
	r, err := s.Journal.PrepareRuntimeConsent(ctx, owner, slug, c)
	if err != nil {
		return RuntimeConsentRecord{}, err
	}
	meta, err := s.exchange(ctx, r, http.MethodPut, key)
	if err != nil {
		return r, err
	}
	return s.Journal.ObserveRuntimeConsent(ctx, owner, slug, id, meta)
}

// Revoke blocks new directory dispatch before I/O and contacts only the
// original retained endpoint, even after registry withdrawal or collection.
func (s RuntimeConsentService) Revoke(ctx context.Context, owner, slug, id, key string) (RuntimeConsentRecord, error) {
	if s.Journal == nil || !ValidID(id) || !api.ValidAPIKeyFormat(key) {
		return RuntimeConsentRecord{}, ErrConflict
	}
	r, err := s.Journal.BeginRevokeRuntimeConsent(ctx, owner, slug, id)
	if err != nil || r.Status == RuntimeConsentRevoked {
		return r, err
	}
	meta, err := s.exchange(ctx, r, http.MethodDelete, key)
	if err != nil {
		return r, err
	}
	if meta.RevokedAt == nil {
		return r, ErrRuntimeConsentUnavailable
	}
	return s.Journal.ObserveRuntimeConsent(ctx, owner, slug, id, meta)
}

func (s RuntimeConsentService) exchange(ctx context.Context, r RuntimeConsentRecord, method, key string) (state.RegionalRuntimeConsent, error) {
	ctx, cancel := context.WithTimeout(ctx, api.GlobalImageReleaseRegionalTimeout)
	defer cancel()
	var body []byte
	if method == http.MethodPut {
		var err error
		body, err = json.Marshal(r.Consent)
		if err != nil {
			return state.RegionalRuntimeConsent{}, ErrRuntimeConsentUnavailable
		}
	}
	b := r.Consent.Binding
	path := b.APIBase + "/v1/cells/" + b.CellID + "/apps/" + b.RegionalAppID + "/runtime-consents/" + r.Consent.ID
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return state.RegionalRuntimeConsent{}, ErrRuntimeConsentUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Verifier.probeClient().Do(req)
	if err != nil {
		return state.RegionalRuntimeConsent{}, ErrRuntimeConsentUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return state.RegionalRuntimeConsent{}, ErrRuntimeConsentUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, api.GlobalDirectoryProofMaxBytes+1))
	if err != nil {
		return state.RegionalRuntimeConsent{}, ErrRuntimeConsentUnavailable
	}
	return decodeRuntimeConsentResponse(raw)
}

// Validate every field, including duplicate outer keys. The nested scope uses
// the existing canonical consent decoder rather than permissive JSON structs.
func decodeRuntimeConsentResponse(raw []byte) (state.RegionalRuntimeConsent, error) {
	var out state.RegionalRuntimeConsent
	if len(raw) == 0 || len(raw) > api.GlobalDirectoryProofMaxBytes {
		return out, ErrRuntimeConsentUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return out, ErrRuntimeConsentUnavailable
	}
	seen := map[string]bool{}
	for d.More() {
		tok, err := d.Token()
		key, ok := tok.(string)
		if err != nil || !ok || seen[key] {
			return out, ErrRuntimeConsentUnavailable
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return out, ErrRuntimeConsentUnavailable
		}
		switch key {
		case "consent":
			out.Consent, err = runtimequota.DecodeConsent(value)
		case "created_at":
			err = json.Unmarshal(value, &out.CreatedAt)
		case "revoked_at":
			var stamp time.Time
			err = json.Unmarshal(value, &stamp)
			if stamp.IsZero() {
				return out, ErrRuntimeConsentUnavailable
			}
			out.RevokedAt = &stamp
		default:
			return out, ErrRuntimeConsentUnavailable
		}
		if err != nil {
			return out, ErrRuntimeConsentUnavailable
		}
	}
	last, err := d.Token()
	if err != nil || last != json.Delim('}') || !seen["consent"] || !seen["created_at"] || out.CreatedAt.IsZero() || d.Decode(&struct{}{}) != io.EOF {
		return out, ErrRuntimeConsentUnavailable
	}
	return out, nil
}
