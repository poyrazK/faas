package toolregistry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func executionToken(value string) bool {
	if len(value) == 0 || len(value) > api.MCPCatalogOAuthMaxTokenBytes || strings.HasPrefix(value, "fp_") || strings.HasPrefix(value, "gmcp_") {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

func executionOAuthSecret(value string) bool {
	if len(value) == 0 || len(value) > api.MCPCatalogOAuthMaxTokenBytes || !utf8.ValidString(value) || strings.HasPrefix(value, "fp_") || strings.HasPrefix(value, "gmcp_") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (e *PrivateExecutor) connectionRedactions(ctx context.Context, g api.ToolGrantRecord) ([]string, error) {
	if g.Credential == nil || g.Credential.ConnectionID == "" {
		return nil, nil
	}
	c, err := connectionForGrant(ctx, e.store, g)
	if err != nil {
		return nil, err
	}
	values, err := e.openConnectionTokens(ctx, c)
	if err != nil {
		return nil, state.ErrToolOAuthReconnect
	}
	out := []string{values["REFRESH_TOKEN"], values["ACCESS_TOKEN"]}
	for _, source := range []struct{ ref, digest string }{{c.Record.Intent.RefreshTokenSecretRef, c.Record.SourceSHA256}, {c.Record.Intent.ClientSecretRef, c.Record.ClientSourceSHA256}} {
		if source.ref != "" {
			value, e := e.connectionSecretValue(ctx, c.Record, source.ref, source.digest)
			if e != nil {
				return nil, e
			}
			out = append(out, value)
		}
	}
	return out, nil
}

func (e *PrivateExecutor) connectionToken(ctx context.Context, g api.ToolGrantRecord) (string, error) {
	store, ok := e.store.(state.ToolOAuthConnectionStore)
	if !ok {
		return "", state.ErrNotFound
	}
	claim, err := store.BeginToolOAuthRenewal(ctx, g.AccountID, g.Credential.ConnectionID, api.ToolInvocationDelegation{GrantID: g.ID, KeyID: g.Intent.KeyID, ProjectID: g.ProjectID})
	if err != nil {
		return "", err
	}
	c := claim.Connection
	if !claim.Fresh {
		values, err := e.openConnectionTokens(ctx, c)
		if err != nil || !executionToken(values["ACCESS_TOKEN"]) {
			return "", state.ErrToolOAuthReconnect
		}
		return values["ACCESS_TOKEN"], nil
	}
	access, ciphertext, expiry, reason := e.renewConnection(ctx, c)
	// A successful response is saved even if the originating request disconnects.
	// Only a durable save grants use of the new access token. Lost responses and
	// expired leases never authorize another exchange of the old refresh token.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ToolOAuthPersistTimeout)
	defer cancel()
	_, err = store.FinishToolOAuthRenewal(persist, g.AccountID, c.Record.ID, c.LeaseID, ciphertext, expiry, reason)
	if err != nil {
		return "", err
	}
	if reason != "" {
		return "", state.ErrToolOAuthReconnect
	}
	return access, nil
}

func (e *PrivateExecutor) connectionSecretValue(ctx context.Context, c api.ToolOAuthConnectionRecord, ref, digest string) (string, error) {
	s, err := connectionSecret(ctx, e.store, c, ref, digest)
	if err != nil {
		return "", err
	}
	values, err := secretbox.OpenMulti(e.opts.Identities(ctx), s.Ciphertext)
	value := values[strings.TrimPrefix(ref, api.SecretRefPrefix)]
	if err != nil || !executionOAuthSecret(value) {
		return "", state.ErrToolOAuthReconnect
	}
	return value, nil
}

func (e *PrivateExecutor) renewConnection(ctx context.Context, c state.ToolOAuthConnection) (string, []byte, *time.Time, string) {
	r := c.Record
	if e.opts.ConnectionRecipients == nil || e.opts.ConnectionIdentities == nil || e.opts.HTTP == nil {
		return "", nil, nil, "credential_unavailable"
	}
	recipients, recipientErr := e.opts.ConnectionRecipients()
	if recipientErr != nil || len(recipients) == 0 {
		return "", nil, nil, "credential_unavailable"
	}
	var refresh string
	var err error
	if len(c.Ciphertext) > 0 {
		values, openErr := e.openConnectionTokens(ctx, c)
		refresh = values["REFRESH_TOKEN"]
		err = openErr
	} else {
		refresh, err = e.connectionSecretValue(ctx, r, r.Intent.RefreshTokenSecretRef, r.SourceSHA256)
	}
	if err != nil || !executionOAuthSecret(refresh) {
		return "", nil, nil, "credential_unavailable"
	}
	secret := ""
	if r.Intent.ClientSecretRef != "" {
		secret, err = e.connectionSecretValue(ctx, r, r.Intent.ClientSecretRef, r.ClientSourceSHA256)
		if err != nil {
			return "", nil, nil, "credential_unavailable"
		}
	}
	response, reason := e.refreshExecutionOAuth(ctx, r.Intent, refresh, secret)
	if reason != "" {
		return "", nil, nil, reason
	}
	return e.sealConnectionResponse(c, response, refresh, recipients)
}

func (e *PrivateExecutor) sealConnectionResponse(c state.ToolOAuthConnection, response executionOAuthResponse, refresh string, recipients []*age.X25519Recipient) (string, []byte, *time.Time, string) {
	r := c.Record
	if response.Scope != "" {
		approved := append([]string{}, r.Intent.Scopes...)
		granted := strings.Fields(response.Scope)
		slices.Sort(approved)
		slices.Sort(granted)
		if !slices.Equal(approved, granted) {
			return "", nil, nil, "scope_changed"
		}
	}
	if response.RefreshToken != "" {
		refresh = response.RefreshToken
	}
	if !executionOAuthSecret(refresh) {
		return "", nil, nil, "renewal_uncertain"
	}
	plain, _ := json.Marshal(secretbox.Envelope{"ACCESS_TOKEN": response.AccessToken, "REFRESH_TOKEN": refresh})
	ciphertext, err := secretbox.SealBytesTo(recipients, "gregale-tool-oauth:"+r.BindingSHA256, plain, api.ToolOAuthCiphertextMaxBytes)
	if err != nil || len(ciphertext) > api.ToolOAuthCiphertextMaxBytes {
		return "", nil, nil, "credential_unavailable"
	}
	expiry := time.Now().UTC().Add(time.Duration(response.ExpiresIn) * time.Second).Truncate(time.Microsecond)
	return response.AccessToken, ciphertext, &expiry, ""
}

func (e *PrivateExecutor) openConnectionTokens(ctx context.Context, c state.ToolOAuthConnection) (secretbox.Envelope, error) {
	if e.opts.ConnectionIdentities == nil {
		return nil, errors.New("execution connection identities unavailable")
	}
	namespace, plain, err := secretbox.OpenBytesMulti(e.opts.ConnectionIdentities(ctx), c.Ciphertext)
	if err != nil || namespace != "gregale-tool-oauth:"+c.Record.BindingSHA256 {
		return nil, errors.New("execution connection envelope mismatch")
	}
	var out secretbox.Envelope
	if json.Unmarshal(plain, &out) != nil {
		return nil, errors.New("execution connection envelope unavailable")
	}
	return out, nil
}

type executionOAuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

func (e *PrivateExecutor) refreshExecutionOAuth(ctx context.Context, in api.ToolOAuthConnectionIntent, refresh, secret string) (executionOAuthResponse, string) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "scope": {strings.Join(in.Scopes, " ")}, "resource": {in.Resource}}
	return e.requestExecutionOAuth(ctx, in, form, secret)
}

func (e *PrivateExecutor) exchangeAuthorization(ctx context.Context, c state.ToolOAuthConnection, code string, values secretbox.Envelope) ([]byte, *time.Time, string) {
	if e.opts.ConnectionRecipients == nil || e.opts.HTTP == nil {
		return nil, nil, "credential_unavailable"
	}
	recipients, err := e.opts.ConnectionRecipients()
	if err != nil || len(recipients) == 0 {
		return nil, nil, "credential_unavailable"
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {values["VERIFIER"]}, "redirect_uri": {values["REDIRECT_URI"]}, "resource": {c.Record.Intent.Resource}}
	response, reason := e.requestExecutionOAuth(ctx, c.Record.Intent, form, "")
	if reason != "" {
		return nil, nil, reason
	}
	_, ciphertext, expiry, reason := e.sealConnectionResponse(c, response, "", recipients)
	return ciphertext, expiry, reason
}

func (e *PrivateExecutor) requestExecutionOAuth(ctx context.Context, in api.ToolOAuthConnectionIntent, form url.Values, secret string) (executionOAuthResponse, string) {
	var out executionOAuthResponse
	work, cancel := context.WithTimeout(ctx, api.ToolOAuthRefreshTimeout)
	defer cancel()
	if in.TokenAuthMethod == "none" {
		form.Set("client_id", in.ClientID)
	}
	req, err := http.NewRequestWithContext(work, http.MethodPost, in.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return out, "credential_unavailable"
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if in.TokenAuthMethod == "client_secret_basic" {
		req.SetBasicAuth(url.QueryEscape(in.ClientID), url.QueryEscape(secret))
	}
	client := *e.opts.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return out, "renewal_uncertain"
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return out, "provider_rejected"
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.ToolOAuthProviderResponseMaxBytes+1))
	if err != nil || len(body) > api.ToolOAuthProviderResponseMaxBytes || json.Unmarshal(body, &out) != nil {
		return executionOAuthResponse{}, "renewal_uncertain"
	}
	if !executionToken(out.AccessToken) || out.RefreshToken != "" && !executionOAuthSecret(out.RefreshToken) || !strings.EqualFold(out.TokenType, "Bearer") || out.ExpiresIn <= 0 || out.ExpiresIn > api.ToolOAuthMaxTokenLifetimeSeconds {
		return executionOAuthResponse{}, "renewal_uncertain"
	}
	return out, ""
}
