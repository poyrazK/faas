package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// authKeyWorld simulates the secrets API and a running Auth app that loads
// its signing keys only when it restarts, as the template does.
type authKeyWorld struct {
	mu       sync.Mutex
	secrets  map[string]string
	created  map[string]string
	running  map[string]any
	restarts int
	// publish is false while a restart has not finished.
	publish bool
}

func newAuthKeyWorld(t *testing.T, secrets map[string]string) *authKeyWorld {
	t.Helper()
	w := &authKeyWorld{secrets: secrets, created: map[string]string{}, publish: true}
	for name := range secrets {
		w.created[name] = "2026-10-14T00:00:00Z"
	}
	w.load()
	old, oldPoll := authAdminHTTPClient, authKeyChangePoll
	authKeyChangePoll = time.Millisecond
	authAdminHTTPClient = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		w.mu.Lock()
		defer w.mu.Unlock()
		if r.URL.Host != "shop-auth.gregale.app" || r.URL.Path != "/settings/v1" || r.Method != http.MethodGet {
			t.Errorf("unexpected Auth app request %s %s", r.Method, r.URL)
		}
		writeJSONTest(recorder, w.running)
		return recorder.Result(), nil
	})}
	t.Cleanup(func() { authAdminHTTPClient, authKeyChangePoll = old, oldPoll })
	authIssuerServer(t, func(rw http.ResponseWriter, r *http.Request) bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		const prefix = "/v1/apps/shop-auth/secrets"
		switch {
		case r.Method == "GET" && r.URL.Path == prefix:
			list := api.AppSecretListResponse{}
			for name := range w.secrets {
				list.Secrets = append(list.Secrets, api.AppSecretResponse{Key: name, Scope: api.DefaultEnvScope, CreatedAt: w.created[name]})
			}
			writeJSONTest(rw, list)
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, prefix+"/"):
			var body api.PutAppSecretRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			name := strings.TrimPrefix(r.URL.Path, prefix+"/")
			w.secrets[name], w.created[name] = body.Value, "2026-10-14T12:00:00Z"
			writeJSONTest(rw, map[string]any{})
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, prefix+"/"):
			delete(w.secrets, strings.TrimPrefix(r.URL.Path, prefix+"/"))
			rw.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && r.URL.Path == "/v1/apps/shop-auth/restart":
			w.restarts++
			if w.publish {
				w.load()
			}
			writeJSONTest(rw, map[string]any{"app_id": "auth-app", "wake_id": "w1"})
		default:
			return false
		}
		return true
	})
	return w
}

// load mirrors config.mjs: every AUTH_JWT_KEY_<ID> verifies and the newest
// AUTH_JWT_SIGNER_<ID> signs.
func (w *authKeyWorld) load() {
	signing, newest, verifying := "", int64(0), []string{}
	for name, value := range w.secrets {
		if id, ok := strings.CutPrefix(name, authSignerPrefix); ok {
			if at, _ := strconv.ParseInt(value, 10, 64); at > newest {
				signing, newest = id, at
			}
		}
	}
	for name := range w.secrets {
		if id, ok := strings.CutPrefix(name, authSigningKeyPrefix); ok && id != signing {
			verifying = append(verifying, id)
		}
	}
	sort.Strings(verifying)
	w.running = map[string]any{"app": "shop.example.com", "keys": map[string]any{"signing": signing, "verifying": verifying}}
}

func initialAuthKeys() map[string]string {
	return map[string]string{authSigningKeyPrefix + "0A1B2C3D": "{}", authSignerPrefix + "0A1B2C3D": "1700000000"}
}

func TestAuthKeysRotateStagesThenActivatesAndKeepsThePreviousKey(t *testing.T) {
	resetJSONEnv(t)
	world := newAuthKeyWorld(t, initialAuthKeys())
	out := captureAuthStdout(t)
	if code := cmdAuth([]string{"keys", "rotate", "shop-auth"}); code != 0 {
		t.Fatalf("rotate = %d: %s", code, out)
	}
	var added string
	for name := range world.secrets {
		if id, ok := strings.CutPrefix(name, authSigningKeyPrefix); ok && id != "0A1B2C3D" {
			added = id
		}
	}
	if added == "" || world.secrets[authSignerPrefix+added] == "" || world.secrets[authSignerPrefix+"0A1B2C3D"] != "" {
		t.Fatalf("markers after rotate: %v", world.secrets)
	}
	var jwk map[string]string
	if err := json.Unmarshal([]byte(world.secrets[authSigningKeyPrefix+added]), &jwk); err != nil || jwk["kid"] != authKeyKID(added) || jwk["d"] == "" {
		t.Fatalf("new key secret: %v %v", jwk, err)
	}
	keys := world.running["keys"].(map[string]any)
	if world.restarts != 2 || keys["signing"] != added || fmt.Sprint(keys["verifying"]) != "[0A1B2C3D]" {
		t.Fatalf("restarts=%d running=%v", world.restarts, world.running)
	}
	if !strings.Contains(out.String(), "gregale auth keys revoke shop-auth 0A1B2C3D") || strings.Contains(out.String(), jwk["d"]) {
		t.Fatalf("output = %s", out)
	}
}

func TestAuthKeysRefuseUnsafeChanges(t *testing.T) {
	resetJSONEnv(t)
	world := newAuthKeyWorld(t, initialAuthKeys())
	captureAuthStdout(t)
	// The signer cannot be revoked, and neither can a key with a marker.
	if code := cmdAuth([]string{"keys", "revoke", "shop-auth", "0A1B2C3D"}); code == 0 || world.secrets[authSigningKeyPrefix+"0A1B2C3D"] == "" {
		t.Fatal("revoked the signing key")
	}
	// A standby key the running app has not loaded cannot sign.
	world.publish = false
	if code := cmdAuth([]string{"keys", "create", "shop-auth"}); code != 0 {
		t.Fatal("create failed")
	}
	var standby string
	for name := range world.secrets {
		if id, ok := strings.CutPrefix(name, authSigningKeyPrefix); ok && id != "0A1B2C3D" {
			standby = id
		}
	}
	if code := cmdAuth([]string{"keys", "activate", "shop-auth", standby}); code == 0 || world.secrets[authSignerPrefix+standby] != "" {
		t.Fatal("activated an unpublished key")
	}
	world.publish = true
	world.load()
	if code := cmdAuth([]string{"keys", "activate", "shop-auth", standby}); code != 0 {
		t.Fatal("activate failed")
	}
	// The previous key no longer signs or has a marker, so it can go.
	if code := cmdAuth([]string{"keys", "revoke", "shop-auth", "0A1B2C3D"}); code != 0 {
		t.Fatal("revoke failed")
	}
	if _, ok := world.secrets[authSigningKeyPrefix+"0A1B2C3D"]; ok {
		t.Fatal("previous key kept")
	}
	if code := cmdAuth([]string{"keys", "revoke", "shop-auth", "FFFFFFFF"}); code == 0 {
		t.Fatal("revoked an unknown key")
	}
	for world.restarts < 20 && countAuthKeys(world.secrets) < api.AuthMaxSigningKeys {
		if code := cmdAuth([]string{"keys", "create", "shop-auth"}); code != 0 {
			t.Fatal("create failed below the bound")
		}
	}
	if code := cmdAuth([]string{"keys", "create", "shop-auth"}); code == 0 || countAuthKeys(world.secrets) != api.AuthMaxSigningKeys {
		t.Fatalf("created more than %d keys", api.AuthMaxSigningKeys)
	}
}

func countAuthKeys(secrets map[string]string) int {
	n := 0
	for name := range secrets {
		if authSigningKeyName.MatchString(name) {
			n++
		}
	}
	return n
}

func TestAuthKeysListReportsStatesAsJSON(t *testing.T) {
	resetJSONEnv(t)
	world := newAuthKeyWorld(t, initialAuthKeys())
	world.publish = false
	world.secrets[authSigningKeyPrefix+"FFFF0001"] = "{}"
	out := captureAuthStdout(t)
	jsonOutput = true
	t.Cleanup(resetJSONOutput) // later tests expect text output
	if code := cmdAuth([]string{"keys", "list", "shop-auth"}); code != 0 {
		t.Fatalf("list = %d", code)
	}
	var body struct {
		Keys []authKeyRow `json:"keys"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("json = %s: %v", out, err)
	}
	states := map[string]string{}
	for _, row := range body.Keys {
		states[row.ID] = row.State + " " + row.KID
	}
	if states["0A1B2C3D"] != "signing gregale-0a1b2c3d" || states["FFFF0001"] != "pending gregale-ffff0001" || len(states) != 2 {
		t.Fatalf("states = %v", states)
	}
}
