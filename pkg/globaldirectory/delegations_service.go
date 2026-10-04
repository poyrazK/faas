package globaldirectory

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type DelegationService struct {
	Store     Store
	Journal   DelegationStore
	Keys      DelegationKeys
	Verifier  Verifier
	Authorize func(context.Context, string) error
}

func (c *DelegationService) authorize(ctx context.Context, owner string) error {
	if c.Authorize == nil {
		return ErrDelegationUnavailable
	}
	return c.Authorize(ctx, owner)
}

func (c *DelegationService) check(ctx context.Context, owner, slug string, r Release, t ReleaseTarget, g Delegation) error {
	if err := c.authorize(ctx, owner); err != nil {
		return err
	}
	if err := c.Store.CheckReleaseTarget(ctx, owner, slug, r.ReleaseID, t.Region); err != nil {
		return err
	}
	return c.Journal.CheckDelegation(ctx, owner, r.GlobalAppID, g.ID)
}
func (c *DelegationService) prepare(ctx context.Context, owner, slug string, r Release, t ReleaseTarget, admin string) (Delegation, error) {
	g, err := c.Journal.CurrentDelegation(ctx, owner, slug, t.Region)
	if err == nil {
		return g, nil
	}
	if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrStaleCell) {
		return Delegation{}, err
	}
	if admin == "" {
		return Delegation{}, ErrDelegationUnavailable
	}
	if err := c.authorize(ctx, owner); err != nil {
		return Delegation{}, err
	}
	if err := c.Store.CheckReleaseTarget(ctx, owner, slug, r.ReleaseID, t.Region); err != nil {
		return Delegation{}, err
	}
	cell := Cell{ID: t.CellID, Region: t.Region, APIBase: t.APIBase, Generation: t.CellGeneration, Enabled: true}
	proof, err := c.Verifier.Verify(ctx, cell, t.RegionalAppSlug, admin)
	if err != nil || proof != t.Binding {
		return Delegation{}, ErrDelegationUnavailable
	}
	app, err := c.Store.AppBySlug(ctx, owner, slug)
	if err != nil {
		return Delegation{}, err
	}
	g, err = c.Keys.Mint(app, t.Target)
	if err != nil {
		return Delegation{}, err
	}
	return c.Journal.EnsureDelegation(ctx, owner, slug, g)
}

// CheckCredential rechecks consent immediately before each regional dispatch.
func (c *DelegationService) CheckCredential(ctx context.Context, owner, slug string, r Release, t ReleaseTarget) error {
	g, err := c.Journal.CurrentDelegation(ctx, owner, slug, t.Region)
	if err != nil {
		return err
	}
	return c.check(ctx, owner, slug, r, t, g)
}

// Credential never retains the admin key. The journal is committed before PUT.
func (c *DelegationService) Credential(ctx context.Context, owner, slug string, r Release, t ReleaseTarget, admin string) (string, error) {
	for range api.GlobalDelegationConsentAttempts {
		g, err := c.prepare(ctx, owner, slug, r, t, admin)
		if err != nil {
			return "", err
		}
		if err := c.check(ctx, owner, slug, r, t, g); err != nil {
			return "", err
		}
		token, err := c.Keys.Open(g)
		if err != nil {
			return "", err
		}
		meta, err := c.grant(ctx, g, http.MethodGet, token, nil)
		if err != nil && admin != "" {
			if err := c.check(ctx, owner, slug, r, t, g); err != nil {
				return "", err
			}
			body := api.PutRegionalReleaseGrantRequest{GlobalAppID: g.GlobalAppID, TokenSHA256: hex.EncodeToString(g.Hash), ExpiresAt: g.ExpiresAt}
			meta, err = c.grant(ctx, g, http.MethodPut, admin, &body)
		}
		if err != nil {
			return "", ErrDelegationUnavailable
		}
		if meta.Status == DelegationRevoked || meta.Status == DelegationExpired {
			if err := c.Journal.SetDelegationStatus(ctx, owner, g.GlobalAppID, g.ID, DelegationRevoked); err != nil {
				return "", err
			}
			if admin != "" {
				continue
			}
			return "", ErrDelegationUnavailable
		}
		if meta.Status != DelegationActive {
			return "", ErrDelegationUnavailable
		}
		if err := c.check(ctx, owner, slug, r, t, g); err != nil {
			return "", err
		}
		if err := c.Journal.SetDelegationStatus(ctx, owner, g.GlobalAppID, g.ID, DelegationActive); err != nil {
			return "", err
		}
		return token, nil
	}
	return "", ErrDelegationUnavailable
}
func (c *DelegationService) grant(ctx context.Context, g Delegation, method, key string, body *api.PutRegionalReleaseGrantRequest) (api.RegionalReleaseGrantResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, api.GlobalImageReleaseRegionalTimeout)
	defer cancel()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return api.RegionalReleaseGrantResponse{}, ErrDelegationUnavailable
		}
	}
	path := g.APIBase + "/v1/cells/" + g.Binding.CellID + "/apps/" + g.Binding.RegionalAppID + "/release-grants/" + g.ID
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(data))
	if err != nil {
		return api.RegionalReleaseGrantResponse{}, ErrDelegationUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Verifier.probeClient().Do(req)
	if err != nil {
		return api.RegionalReleaseGrantResponse{}, ErrDelegationUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return api.RegionalReleaseGrantResponse{}, ErrDelegationUnavailable
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, api.GlobalDirectoryProofMaxBytes+1))
	if err != nil || len(data) > api.GlobalDirectoryProofMaxBytes {
		return api.RegionalReleaseGrantResponse{}, ErrDelegationUnavailable
	}
	var out api.RegionalReleaseGrantResponse
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(&struct{}{}) != io.EOF || out.GrantID != g.ID || out.CellID != g.Binding.CellID || out.RegionalAppID != g.Binding.RegionalAppID || out.GlobalAppID != g.GlobalAppID || !out.ExpiresAt.Equal(g.ExpiresAt) {
		return out, ErrDelegationUnavailable
	}
	return out, nil
}

// Revoke uses the immutable original endpoint only, even after detachment.
// A 401 is retryable: revoked bearers cannot prove their tombstone over HTTP.
func (c *DelegationService) Revoke(ctx context.Context, g Delegation) error {
	current, err := c.Journal.ReadDelegation(ctx, g.AccountID, g.GlobalAppID, g.ID)
	if err != nil {
		return err
	}
	if !current.ExpiresAt.After(time.Now()) {
		return c.Journal.SetDelegationStatus(ctx, g.AccountID, g.GlobalAppID, g.ID, DelegationExpired)
	}
	if current.Status != DelegationRevoking {
		return nil
	}
	token, err := c.Keys.Open(current)
	if err != nil {
		return err
	}
	meta, err := c.grant(ctx, current, http.MethodDelete, token, nil)
	if err != nil || meta.Status != DelegationRevoked && meta.Status != DelegationExpired {
		return ErrDelegationUnavailable
	}
	return c.Journal.SetDelegationStatus(ctx, g.AccountID, g.GlobalAppID, g.ID, DelegationRevoked)
}
