// adr: 1190 — the dashboard manages an Auth app's signing keys as
// `gregale auth keys` does: each key is its own sealed secret and the newest
// AUTH_JWT_SIGNER_<ID> marker names the signer. The page reads the running
// app's /settings/v1 and offers each step only when the running app is ready
// for it (activate a key it publishes, revoke a key it only verifies with);
// apid checks the sealed state. Private keys are generated here, sealed like
// any secret and never shown.
package apidserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authsettings"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardAuthKeys is the page's view of the sealed keys.
func (s *server) dashboardAuthKeys(ctx context.Context, acct state.Account, app state.App) []dashboard.AuthKey {
	secrets, err := s.store.ListAppSecretsInScope(ctx, acct.ID, app.ID, api.DefaultEnvScope)
	if err != nil {
		return nil
	}
	created := map[string]string{}
	for _, secret := range secrets {
		created[secret.Key] = secret.CreatedAt.UTC().Format(time.RFC3339)
	}
	keys := []dashboard.AuthKey{}
	for _, key := range authsettings.SealedKeys(created) {
		keys = append(keys, dashboard.AuthKey{ID: key.ID, CreatedAt: key.CreatedAt, Signer: key.Signer})
	}
	return keys
}

// sealedAuthKeys reads the sealed keys and signer markers through the
// secrets API handler, as the writes that follow go through it too.
func (s *server) sealedAuthKeys(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) ([]authsettings.SealedKey, []string, bool) {
	resp := s.forwardDashboardJSON(r, acct, http.MethodGet, "/v1/apps/"+url.PathEscape(app.Slug)+"/secrets", api.DefaultEnvScope, "", nil, s.listSecrets)
	if !dashboardMutationSucceeded(w, resp) {
		return nil, nil, false
	}
	var list api.AppSecretListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		renderProblem(w, s.log, err)
		return nil, nil, false
	}
	created, names := map[string]string{}, []string{}
	for _, secret := range list.Secrets {
		created[secret.Key] = secret.CreatedAt
		names = append(names, secret.Key)
	}
	return authsettings.SealedKeys(created), authsettings.SignerMarkers(names), true
}

// dashboardAuthKeyCreate adds a standby key: the restarted app publishes it
// for verification, so every verifier can learn it before it signs.
func (s *server) dashboardAuthKeyCreate(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardAuthSettingsApp(w, r)
	if !ok {
		return
	}
	keys, _, ok := s.sealedAuthKeys(w, r, acct, app)
	if !ok {
		return
	}
	if len(keys) >= api.AuthMaxSigningKeys {
		redirectDashboardAuth(w, r, app.Slug, "", fmt.Sprintf("%s already has %d signing keys, the most allowed. Revoke a previous key first.", app.Slug, len(keys)))
		return
	}
	id, jwk, err := authsettings.NewSigningKey()
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	s.applyDashboardAuthSecrets(w, r, acct, app, map[string]string{authsettings.SigningKeyPrefix + id: jwk}, nil, "key-added")
}

// dashboardAuthKeyActivate makes a key sign. The new marker is written
// before the old ones are removed, so a restart at any point finds a signer.
func (s *server) dashboardAuthKeyActivate(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardAuthSettingsApp(w, r)
	if !ok {
		return
	}
	key, markers, ok := s.sealedAuthKey(w, r, acct, app)
	if !ok {
		return
	}
	stale := []string{}
	for _, marker := range markers {
		if marker != authsettings.SignerPrefix+key.ID {
			stale = append(stale, marker)
		}
	}
	if key.Signer && len(stale) == 0 {
		redirectDashboardAuth(w, r, app.Slug, "unchanged", "")
		return
	}
	set := map[string]string{authsettings.SignerPrefix + key.ID: strconv.FormatInt(time.Now().Unix(), 10)}
	s.applyDashboardAuthSecrets(w, r, acct, app, set, stale, "key-activated")
}

// dashboardAuthKeyRevoke removes a key that does not sign. Tokens it signed
// stop verifying, which is how a leaked key is cut off.
func (s *server) dashboardAuthKeyRevoke(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardAuthSettingsApp(w, r)
	if !ok {
		return
	}
	key, _, ok := s.sealedAuthKey(w, r, acct, app)
	if !ok {
		return
	}
	if key.Signer {
		redirectDashboardAuth(w, r, app.Slug, "", "Key "+key.ID+" signs, or is being made the signer. Activate another key first.")
		return
	}
	s.applyDashboardAuthSecrets(w, r, acct, app, nil, []string{authsettings.SigningKeyPrefix + key.ID}, "key-revoked")
}

// sealedAuthKey finds the key the path names, answering 404 when the app has
// no such key.
func (s *server) sealedAuthKey(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) (authsettings.SealedKey, []string, bool) {
	id := r.PathValue("id")
	if !authsettings.KeyID.MatchString(id) {
		http.NotFound(w, r)
		return authsettings.SealedKey{}, nil, false
	}
	keys, markers, ok := s.sealedAuthKeys(w, r, acct, app)
	if !ok {
		return authsettings.SealedKey{}, nil, false
	}
	for _, key := range keys {
		if key.ID == id {
			return key, markers, true
		}
	}
	http.NotFound(w, r)
	return authsettings.SealedKey{}, nil, false
}
