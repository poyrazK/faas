package globaldirectory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Errors deliberately omit remote bodies and transport diagnostics: either
// may echo the one-request credential supplied by a caller.
var ErrVerification = errors.New("could not verify regional app ownership")

type Verifier struct{ Client *http.Client }

func (v Verifier) Verify(ctx context.Context, cell Cell, slug, token string) (Binding, error) {
	if cell.Validate() != nil || !cell.Enabled || cell.Generation < 1 || !ValidSlug(slug) || (!api.ValidAPIKeyFormat(token) && !api.ValidRegionalReleaseTokenFormat(token)) {
		return Binding{}, ErrVerification
	}
	client := v.probeClient()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, cell.APIBase+"/v1/cell/apps/"+url.PathEscape(slug)+"/binding", nil)
	if err != nil {
		return Binding{}, ErrVerification
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return Binding{}, ErrVerification
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Binding{}, ErrVerification
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.GlobalDirectoryProofMaxBytes+1))
	if err != nil || len(body) > api.GlobalDirectoryProofMaxBytes {
		return Binding{}, ErrVerification
	}
	var proof api.CellAppBindingResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&proof) != nil || decoder.Decode(&struct{}{}) != io.EOF || proof.CellID != cell.ID || proof.Region != cell.Region || proof.AppSlug != slug {
		return Binding{}, ErrVerification
	}
	binding := Binding{CellID: proof.CellID, Region: proof.Region, CellGeneration: cell.Generation, RegionalAccountID: proof.AccountID, RegionalOrgID: proof.OrgID, RegionalAppID: proof.AppID, RegionalAppSlug: proof.AppSlug}
	if binding.Validate() != nil {
		return Binding{}, ErrVerification
	}
	return binding, nil
}

func (v Verifier) probeClient() *http.Client {
	client := http.Client{Timeout: api.GlobalDirectoryProofTimeoutSeconds * time.Second}
	if v.Client != nil {
		client = *v.Client
	}
	if client.Timeout <= 0 || client.Timeout > api.GlobalDirectoryProofTimeoutSeconds*time.Second {
		client.Timeout = api.GlobalDirectoryProofTimeoutSeconds * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}
