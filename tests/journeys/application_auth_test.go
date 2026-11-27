package journeys

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Application Auth (ADR-1190) is dark-launched: the CLI shows its commands
// only with this variable, and apid serves it only to accounts the
// application_auth platform flag admits. The journey skips while the
// account's `gregale feature list` reports application-auth hidden.
var applicationAuthCLI = []string{"FAAS_APPLICATION_AUTH_ENABLED=1"}

// authJourney is the state TestApplicationAuth's steps share.
type authJourney struct {
	tg       *target
	database string
	app      string
	base     string
	email    string
	password string
	adminKey string
	deleted  bool
}

// TestApplicationAuth is a customer's Auth journey through the CLI and the
// Auth app's public URL: create a managed database and an Auth app on it,
// check the published ES256 keys and secure password defaults, sign up,
// check that repeated wrong passwords are held, and check that a read-only
// admin key reads users but cannot change them.
func TestApplicationAuth(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	skipHidden(t, hiddenCapabilities(t, tg), "application-auth")
	j := &authJourney{tg: tg, password: "Journey-" + tg.runID + "-Battery-Staple-7"}
	j.email = "auth-journey-" + strings.ToLower(tg.runID) + "@example.com"
	tg.addSensitive(j.password)
	j.app = tg.app(t, "auth")
	j.base = tg.appURL(j.app)
	j.database = tg.slug("auth-db")
	// The app's database binding refuses a plain app delete, so this runs
	// before tg.app's cleanup (cleanups run last-registered first), then the
	// database goes.
	t.Cleanup(func() { j.cleanupDatabase(t) })
	t.Cleanup(func() { j.cleanupApp(t) })
	s := newStory(t)
	s.step("create_database", j.createDatabase)
	s.step("create_auth_app", j.createApp)
	s.check("es256_jwks", j.jwks)
	s.check("secure_password_defaults", j.passwordDefaults)
	s.step("sign_up", j.signUp)
	s.check("wrong_passwords_are_held", j.attemptBudget)
	s.check("scoped_admin_key", j.scopedAdminKey)
}

func (j *authJourney) createDatabase(t *testing.T) {
	region := os.Getenv("GREGALE_E2E_POSTGRES_REGION")
	if region == "" {
		region = "us-east-2"
	}
	j.tg.ok(t, 5*time.Minute, "postgres", "create", j.database, "--region", region, "--class", "development", "--scale-to-zero")
	eventually(t, "database "+j.database+" ready", 15*time.Minute, 15*time.Second, func() (bool, string) {
		res := j.tg.run(t, time.Minute, "postgres", "get", j.database)
		if res.exit != 0 {
			return false, res.problem()
		}
		doc := object(t, res.stdout)
		return str(doc, "state") == "ready", "state " + str(doc, "state")
	})
}

func (j *authJourney) createApp(t *testing.T) {
	j.tg.runWithEnvOK(t, applicationAuthCLI, 25*time.Minute, "auth", "create", j.app,
		"--database", j.database, "--site-url", "https://example.com", "--autoconfirm")
	eventually(t, "Auth app "+j.app+" serving", 5*time.Minute, 10*time.Second, func() (bool, string) {
		r := get(j.base + "/auth/v1/health")
		return r.status == http.StatusOK, r.String()
	})
}

// jwks checks that the app publishes only public ES256 keys.
func (j *authJourney) jwks(t *testing.T) {
	doc := jsonBody(t, get(j.base+"/auth/v1/.well-known/jwks.json"))
	keys, _ := field(doc, "keys").([]any)
	if len(keys) == 0 {
		t.Fatalf("the JWKS lists no keys: %s", compact(doc))
	}
	for _, k := range keys {
		if str(k, "kty") != "EC" || str(k, "crv") != "P-256" || field(k, "d") != nil {
			t.Errorf("JWKS key is not a public P-256 key: kty=%s crv=%s private=%v", str(k, "kty"), str(k, "crv"), field(k, "d") != nil)
		}
	}
}

// passwordDefaults checks the 8-character minimum and the leaked-password
// check new apps get, and that GoTrue enforces the minimum.
func (j *authJourney) passwordDefaults(t *testing.T) {
	doc := jsonBody(t, get(j.base+"/settings/v1"))
	if num(doc, "password.min_length") < 8 || str(doc, "password.leaked_password_check") != "true" {
		t.Errorf("password settings = %s, want min_length >= 8 and leaked_password_check true", compact(field(doc, "password")))
	}
	r := j.post("/auth/v1/signup", map[string]string{"email": "short-" + j.email, "password": "Ab1!xyz"})
	if r.status != http.StatusUnprocessableEntity || str(jsonBody(t, r), "error_code") != "weak_password" {
		t.Errorf("sign-up with a 7-character password = %s, want 422 weak_password", r)
	}
}

func (j *authJourney) signUp(t *testing.T) {
	r := j.post("/auth/v1/signup", map[string]string{"email": j.email, "password": j.password})
	if r.status != http.StatusOK || str(jsonBody(t, r), "access_token") == "" {
		t.Fatalf("sign-up = %s, want 200 with a session", j.tg.redact(r.String()))
	}
}

// attemptBudget sends wrong passwords for the account until the front holds
// one (10 per account and address in 15 minutes), then checks that the hold
// also refuses the right password and says when to retry.
func (j *authJourney) attemptBudget(t *testing.T) {
	path := "/auth/v1/token?grant_type=password"
	for i := 0; i < 10; i++ {
		r := j.post(path, map[string]string{"email": j.email, "password": "wrong-password-" + j.tg.runID})
		if r.status == http.StatusTooManyRequests {
			t.Fatalf("attempt %d held before 10 wrong passwords: %s", i+1, r)
		}
		if r.status != http.StatusBadRequest {
			t.Fatalf("wrong password %d = %s, want 400 invalid_credentials", i+1, r)
		}
	}
	r := j.post(path, map[string]string{"email": j.email, "password": j.password})
	if r.status != http.StatusTooManyRequests || str(jsonBody(t, r), "error_code") != "too_many_attempts" || r.header.Get("Retry-After") == "" {
		t.Fatalf("sign-in after 10 wrong passwords = %s, want 429 too_many_attempts with Retry-After", j.tg.redact(r.String()))
	}
}

// scopedAdminKey creates a users:read key that expires in a day, waits for
// the restarted app to accept it, and checks it reads users but cannot sign
// one out.
func (j *authJourney) scopedAdminKey(t *testing.T) {
	doc := object(t, j.tg.runWithEnvOK(t, applicationAuthCLI, 5*time.Minute, "auth", "admin-key", "create", j.app,
		"--scopes", "users:read", "--expires-in", "1").stdout)
	j.adminKey = str(doc, "key")
	j.tg.addSensitive(j.adminKey)
	if j.adminKey == "" || str(doc, "expires_at") == "" {
		t.Fatalf("admin-key create did not return a key with an expiry: scopes=%s", compact(field(doc, "scopes")))
	}
	auth := map[string]string{"Authorization": "Bearer " + j.adminKey}
	var users httpResult
	eventually(t, "the restarted Auth app accepts the new key", 5*time.Minute, 10*time.Second, func() (bool, string) {
		users = fetch(http.MethodGet, j.base+"/admin/v1/users?search="+url.QueryEscape(j.email), auth)
		return users.status == http.StatusOK, j.tg.redact(users.String())
	})
	list, _ := field(jsonBody(t, users), "users").([]any)
	if len(list) != 1 {
		t.Fatalf("the admin user list finds %d users for the journey's address, want 1", len(list))
	}
	id := str(list[0], "id")
	r := send(http.MethodPost, j.base+"/admin/v1/users/"+id+"/sign-out", map[string]string{"Authorization": auth["Authorization"], "Content-Type": "application/json"}, []byte(`{}`))
	if r.status != http.StatusForbidden || str(jsonBody(t, r), "code") != "admin_key_scope" {
		t.Errorf("sign-out with a users:read key = %s, want 403 admin_key_scope", j.tg.redact(r.String()))
	}
	keys := object(t, j.tg.runWithEnvOK(t, applicationAuthCLI, time.Minute, "auth", "admin-key", "list", j.app).stdout)
	rows, _ := field(keys, "admin_keys").([]any)
	if len(rows) != 1 || compact(field(rows[0], "scopes")) != `["users:read"]` {
		t.Errorf("admin-key list = %s, want one users:read key", compact(keys))
	}
}

func (j *authJourney) post(path string, body any) httpResult {
	raw, _ := json.Marshal(body)
	return send(http.MethodPost, j.base+path, map[string]string{"Content-Type": "application/json"}, raw)
}

// cleanupApp revokes the Auth app's database binding and deletes it, so
// tg.app's cleanup that runs next finds nothing left.
func (j *authJourney) cleanupApp(t *testing.T) {
	if j.deleted || (j.tg.keep && t.Failed()) {
		return
	}
	if res := j.tg.run(t, 5*time.Minute, "app", "delete", j.app, "--delete-bindings", "--yes"); res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
		t.Logf("warning: cleanup could not delete the Auth app %s (%s); the next run's sweeper removes it", j.app, res.problem())
	}
	j.deleted = true
}

func (j *authJourney) cleanupDatabase(t *testing.T) {
	if j.tg.keep && t.Failed() {
		return
	}
	if res := j.tg.run(t, 5*time.Minute, "postgres", "delete", j.database, "--purge", "--yes"); res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
		t.Logf("warning: cleanup could not delete the database %s (%s)", j.database, res.problem())
	}
}
