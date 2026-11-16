package toolregistry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolconnection"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func (e *PrivateExecutor) AuthorizationHandler() httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		if !connectionOwnerAccess(w, r, acct, e.store) {
			return
		}
		id := r.PathValue("id")
		connections, ok := e.store.(state.ToolOAuthConnectionStore)
		if !ok || !toolgrant.ValidID(id) {
			connectionRespond(w, nil, state.ErrInvalidArgument)
			return
		}
		c, err := connections.GetToolOAuthConnection(r.Context(), acct.ID, id)
		if err != nil {
			connectionRespond(w, nil, err)
			return
		}
		if !c.Record.Intent.Guided {
			connectionRespond(w, nil, state.ErrInvalidArgument)
			return
		}
		if r.Method == http.MethodGet {
			out, err := e.authorizationStatus(r.Context(), connections, c)
			connectionRespond(w, out, err)
			return
		}
		if err = connectionAuthority(r.Context(), e.store, c); err != nil {
			connectionRespond(w, nil, err)
			return
		}
		var in api.ToolOAuthAuthorizationRequest
		if httpkit.DecodeJSONSized(r, &in, api.ToolOAuthAuthorizationRequestMaxBytes) != nil {
			connectionRespond(w, nil, state.ErrInvalidArgument)
			return
		}
		var out api.ToolOAuthAuthorizationResponse
		switch in.Action {
		case "begin":
			out, err = e.beginAuthorization(r.Context(), connections, c, in)
		case "complete":
			out, err = e.completeAuthorization(r.Context(), connections, c, in)
		default:
			err = state.ErrInvalidArgument
		}
		if err == nil && e.opts.Audit != nil {
			e.opts.Audit(r.Context(), "tool_oauth_connection.authorization", acct.ID, map[string]any{"connection_id": id, "app_id": c.Record.AppID, "state": out.State})
		}
		connectionRespond(w, out, err)
	}
}

func (e *PrivateExecutor) authorizationStatus(ctx context.Context, store state.ToolOAuthConnectionStore, c state.ToolOAuthConnection) (api.ToolOAuthAuthorizationResponse, error) {
	out := api.ToolOAuthAuthorizationResponse{ConnectionID: c.Record.ID, State: "not_started"}
	view := ownerConnectionView(ctx, e.store, c)
	if view.State == "revoked" || view.State == "reconnect_required" {
		out.State = "reconnect_required"
		return out, nil
	}
	if c.Record.Generation > 0 {
		out.State = "connected"
		return out, nil
	}
	a, err := store.GetToolOAuthAuthorization(ctx, c.Record.AccountID, c.Record.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return out, nil
		}
		return out, err
	}
	out.ExpiresAt = &a.ExpiresAt
	switch a.Phase {
	case "pending":
		out.State = "authorization_pending"
	case "exchanging":
		out.State = "authorization_uncertain"
	default:
		out.State = "reconnect_required"
	}
	return out, nil
}

func (e *PrivateExecutor) beginAuthorization(ctx context.Context, store state.ToolOAuthConnectionStore, c state.ToolOAuthConnection, in api.ToolOAuthAuthorizationRequest) (api.ToolOAuthAuthorizationResponse, error) {
	out := api.ToolOAuthAuthorizationResponse{ConnectionID: c.Record.ID}
	if !toolconnection.ValidCallback(in.RedirectURI) || in.State != "" || in.Code != "" || in.Denied || in.Issuer != "" {
		return out, state.ErrInvalidArgument
	}
	if e.opts.ConnectionRecipients == nil || e.opts.ConnectionIdentities == nil || e.opts.HTTP == nil {
		return out, state.ErrToolOAuthReconnect
	}
	recipients, err := e.opts.ConnectionRecipients()
	if err != nil || len(recipients) == 0 {
		return out, state.ErrToolOAuthReconnect
	}
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return out, errors.New("authorization entropy unavailable")
	}
	callbackState := base64.RawURLEncoding.EncodeToString(nonce)
	if _, err = rand.Read(nonce); err != nil {
		return out, errors.New("authorization entropy unavailable")
	}
	verifier := base64.RawURLEncoding.EncodeToString(nonce)
	plain, _ := json.Marshal(secretbox.Envelope{"STATE": callbackState, "VERIFIER": verifier, "REDIRECT_URI": in.RedirectURI})
	sealed, err := secretbox.SealBytesTo(recipients, "gregale-tool-oauth-authorization:"+c.Record.BindingSHA256, plain, api.ToolOAuthAuthorizationCiphertextMaxBytes)
	if err != nil {
		return out, state.ErrToolOAuthReconnect
	}
	a, err := store.BeginToolOAuthAuthorization(ctx, c.Record.AccountID, c.Record.ID, state.ToolOAuthAuthorization{AccountID: c.Record.AccountID, ConnectionID: c.Record.ID, StateSHA256: toolpublication.Digest([]byte(callbackState)), Phase: "pending", ExpiresAt: time.Now().UTC().Add(api.ToolOAuthAuthorizationLifetime).Truncate(time.Microsecond), Ciphertext: sealed})
	if err != nil {
		return out, err
	}
	values, err := e.openAuthorization(ctx, c, a)
	if err != nil || values["REDIRECT_URI"] != in.RedirectURI {
		return out, state.ErrConflict
	}
	u, _ := url.Parse(c.Record.Intent.AuthorizationEndpoint)
	q := u.Query()
	sum := sha256.Sum256([]byte(values["VERIFIER"]))
	q.Set("response_type", "code")
	q.Set("client_id", c.Record.Intent.ClientID)
	q.Set("redirect_uri", in.RedirectURI)
	q.Set("state", values["STATE"])
	q.Set("code_challenge_method", "S256")
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("scope", strings.Join(c.Record.Intent.Scopes, " "))
	q.Set("resource", c.Record.Intent.Resource)
	u.RawQuery = q.Encode()
	out.State = "authorization_pending"
	out.AuthorizationURL = u.String()
	out.CallbackState = values["STATE"]
	out.ExpiresAt = &a.ExpiresAt
	return out, nil
}

func (e *PrivateExecutor) openAuthorization(ctx context.Context, c state.ToolOAuthConnection, a state.ToolOAuthAuthorization) (secretbox.Envelope, error) {
	if e.opts.ConnectionIdentities == nil {
		return nil, state.ErrToolOAuthReconnect
	}
	namespace, plain, err := secretbox.OpenBytesMulti(e.opts.ConnectionIdentities(ctx), a.Ciphertext)
	if err != nil || namespace != "gregale-tool-oauth-authorization:"+c.Record.BindingSHA256 {
		return nil, state.ErrToolOAuthReconnect
	}
	var out secretbox.Envelope
	if json.Unmarshal(plain, &out) != nil || !toolconnection.ValidAuthorizationState(out["STATE"]) || toolpublication.Digest([]byte(out["STATE"])) != a.StateSHA256 || !toolconnection.ValidAuthorizationState(out["VERIFIER"]) || !toolconnection.ValidCallback(out["REDIRECT_URI"]) {
		return nil, state.ErrToolOAuthReconnect
	}
	return out, nil
}

func (e *PrivateExecutor) completeAuthorization(ctx context.Context, store state.ToolOAuthConnectionStore, c state.ToolOAuthConnection, in api.ToolOAuthAuthorizationRequest) (api.ToolOAuthAuthorizationResponse, error) {
	out := api.ToolOAuthAuthorizationResponse{ConnectionID: c.Record.ID}
	if !toolconnection.ValidAuthorizationState(in.State) || in.RedirectURI != "" || in.Issuer != "" && in.Issuer != c.Record.Intent.Issuer || in.Denied && in.Code != "" || !in.Denied && !executionToken(in.Code) {
		return out, state.ErrInvalidArgument
	}
	a, err := store.GetToolOAuthAuthorization(ctx, c.Record.AccountID, c.Record.ID)
	if err != nil {
		return out, err
	}
	if a.Phase != "pending" {
		return out, state.ErrConflict
	}
	values, err := e.openAuthorization(ctx, c, a)
	if err != nil {
		return out, err
	}
	claimed, err := store.ConsumeToolOAuthAuthorization(ctx, c.Record.AccountID, c.Record.ID, toolpublication.Digest([]byte(in.State)))
	if err != nil {
		return out, err
	}
	var ciphertext []byte
	var expiry *time.Time
	reason := "provider_rejected"
	if !in.Denied {
		ciphertext, expiry, reason = e.exchangeAuthorization(ctx, claimed, in.Code, values)
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ToolOAuthPersistTimeout)
	defer cancel()
	_, err = store.FinishToolOAuthAuthorization(persist, c.Record.AccountID, c.Record.ID, claimed.LeaseID, ciphertext, expiry, reason)
	if err != nil {
		return out, err
	}
	if reason != "" {
		if in.Denied {
			out.State = "authorization_denied"
			return out, nil
		}
		return out, state.ErrToolOAuthReconnect
	}
	out.State = "connected"
	return out, nil
}
