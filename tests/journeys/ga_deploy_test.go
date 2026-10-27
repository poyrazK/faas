package journeys

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// GA inventory section A (deploy and build) and B (releases) journeys
// beyond the core lifecycle: app settings and rename, Dockerfile and
// tarball deploys with in-process secret reload, build provenance, image
// preflight, gregale.yaml overrides, registry credentials and deploy tokens.

// reloadDockerfile builds the probe from a Dockerfile and opts it into
// in-process secret reload with the documented OCI label (docs/secrets.md).
const reloadDockerfile = `FROM node:22-alpine
WORKDIR /app
COPY package.json server.js version.txt ./
LABEL com.gregale.secret-reload-signal="SIGHUP"
ENV PORT=8080
EXPOSE 8080
CMD ["node", "server.js"]
`

// overridesYAML moves the probe to port 9090 and its health check to
// /ready through gregale.yaml (docs/deploy-overrides.md).
const overridesYAML = `hosting:
  start: node server.js
  port: 9090
  health: /ready
`

// TestAppSettingsAndRename updates an app setting, reads it back, and
// renames the app: the old hostname stops routing and the new one serves
// (row 1).
func TestAppSettingsAndRename(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "settings")
	s := newStory(t)

	s.check("update_setting", func(t *testing.T) {
		shown := object(t, tg.ok(t, time.Minute, "app", slug, "--idle", "120").stdout)
		if num(shown, "idle_timeout_s") != 120 {
			t.Fatalf("--idle 120 reads back as %s", compact(field(shown, "idle_timeout_s")))
		}
		again := object(t, tg.ok(t, time.Minute, "app", slug).stdout)
		if num(again, "idle_timeout_s") != 120 {
			t.Fatalf("app %s shows idle_timeout_s %s after the update", slug, compact(field(again, "idle_timeout_s")))
		}
	})
	newSlug := tg.slug("renamed")
	t.Cleanup(func() { tg.run(t, 2*time.Minute, "apps", "rm", newSlug, "--yes") })
	s.step("rename", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "app", slug, "rename", newSlug)
		eventually(t, "old hostname stops routing", 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/")
			return r.status == http.StatusNotFound, r.String()
		})
		expectVersion(t, tg.appURL(newSlug), "v1", 2*time.Minute)
	})
	s.check("renamed_app_reads_back", func(t *testing.T) {
		shown := object(t, tg.ok(t, time.Minute, "app", newSlug).stdout)
		if str(shown, "slug") != newSlug {
			t.Fatalf("renamed app reads back as %s", compact(shown))
		}
		if res := tg.run(t, time.Minute, "app", slug); res.exit == 0 {
			t.Errorf("the old slug %s still resolves after the rename", slug)
		}
	})
}

// TestDockerfileTarballAndSecretReload deploys a source tarball built with
// its own Dockerfile (row 2), then rotates a secret without a restart: the
// app rereads the snapshot on SIGHUP, acknowledges it, and serves the new
// value from the same process; unsetting revokes it (row 61).
func TestDockerfileTarballAndSecretReload(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug := tg.app(t, "reload")
	base := tg.appURL(slug)
	src := probeVariant(t, "v1-docker", map[string]string{"Dockerfile": reloadDockerfile})
	s := newStory(t)

	s.step("dockerfile_tarball_deploy", func(t *testing.T) {
		receipt := tg.deploy(t, slug, "--tarball", tarball(t, src), "--dockerfile")
		if u := str(receipt, "app_url"); u != "" {
			base = strings.TrimRight(u, "/")
		}
		expectVersion(t, base, "v1-docker", 2*time.Minute)
	})
	secretHash := func(t *testing.T) map[string]any {
		t.Helper()
		r := get(base + "/secret?key=E2E_HOT")
		if r.status != http.StatusOK {
			t.Fatalf("GET /secret: %s", r)
		}
		return jsonBody(t, r)
	}
	sha := func(v string) string {
		sum := sha256.Sum256([]byte(v))
		return hex.EncodeToString(sum[:])
	}
	first, second := randomHex(t, 24), randomHex(t, 24)
	var pid string
	s.step("initial_secret_with_restart", func(t *testing.T) {
		tg.ok(t, 5*time.Minute, "secrets", "set", "--app", slug, "E2E_HOT="+first, "--restart")
		eventually(t, "first value delivered", 3*time.Minute, 3*time.Second, func() (bool, string) {
			doc := secretHash(t)
			return str(doc, "sha256") == sha(first), "app sees " + compact(doc)
		})
		pid = str(secretHash(t), "pid")
	})
	s.step("hot_reload_without_restart", func(t *testing.T) {
		tg.ok(t, 4*time.Minute, "secrets", "set", "--app", slug, "E2E_HOT="+second, "--wait-for-ack", "--timeout", "3m")
		eventually(t, "second value served from the reload snapshot", 2*time.Minute, 3*time.Second, func() (bool, string) {
			doc := secretHash(t)
			return str(doc, "sha256") == sha(second) && str(doc, "source") == "reload", "app sees " + compact(doc)
		})
		if now := str(secretHash(t), "pid"); now != pid {
			t.Errorf("the app restarted (pid %s -> %s); the rotation should have reloaded in process", pid, now)
		}
	})
	s.check("revocation", func(t *testing.T) {
		tg.ok(t, 4*time.Minute, "secrets", "unset", "--app", slug, "E2E_HOT", "--wait-for-ack", "--timeout", "3m")
		eventually(t, "revoked secret gone from the app", 2*time.Minute, 3*time.Second, func() (bool, string) {
			doc := secretHash(t)
			present, _ := field(doc, "present").(bool)
			return !present, "app still sees " + compact(doc)
		})
	})
}

// TestBuildsAndProvenance reads the shared probe's build: status, list,
// provenance attestation, SBOM and build log (row 3).
func TestBuildsAndProvenance(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, _ := tg.sharedProbe(t)
	var buildID string
	s := newStory(t)

	s.step("list", func(t *testing.T) {
		docs := objects(t, tg.ok(t, time.Minute, "build", "list", "--app", slug).stdout)
		for _, doc := range docs {
			items, _ := field(doc, "items").([]any)
			if len(items) > 0 {
				buildID = str(items[0], "id")
				break
			}
			if id := str(doc, "id"); id != "" {
				buildID = id
				break
			}
		}
		if buildID == "" {
			t.Fatalf("build list --app %s shows no build", slug)
		}
	})
	for _, sub := range []string{"status", "provenance", "sbom"} {
		s.check(sub, func(t *testing.T) {
			res := tg.ok(t, time.Minute, "build", sub, buildID)
			if len(strings.TrimSpace(string(res.stdout))) == 0 {
				t.Fatalf("build %s %s printed nothing", sub, buildID)
			}
			t.Logf("build %s: %s", sub, tail(string(res.stdout), 300))
		})
	}
	s.check("build_log", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "deploys", "logs", tg.sharedDeployID, "--limit", "200")
		if !strings.Contains(string(res.stdout), "build") {
			t.Fatalf("the deployment log does not mention the build: %s", tail(string(res.stdout), 400))
		}
	})
}

// TestImagePreflight checks `gregale doctor --image` accepts a deployable
// image and rejects one that does not exist (row 4).
func TestImagePreflight(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	t.Run("deployable_image", func(t *testing.T) {
		res := tg.ok(t, 2*time.Minute, "doctor", "--image", probeImage)
		t.Logf("doctor: %s", tail(string(res.stdout), 400))
	})
	t.Run("missing_image_rejected", func(t *testing.T) {
		missing := "docker.io/library/alpine@sha256:" + strings.Repeat("0", 64)
		if res := tg.run(t, 2*time.Minute, "doctor", "--image", missing); res.exit == 0 {
			t.Fatalf("doctor accepted a digest that does not exist: %s", tail(string(res.stdout), 300))
		}
	})
}

// TestDeployOverrides deploys with a gregale.yaml that moves the port to
// 9090 and the health check to /ready; the app must serve on the declared
// port and the smoke check must use the declared path (row 11).
func TestDeployOverrides(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug := tg.app(t, "overrides")
	receipt := tg.deploy(t, slug, "--path", probeVariant(t, "v1-yaml", map[string]string{"gregale.yaml": overridesYAML}))
	base := strings.TrimRight(str(receipt, "app_url"), "/")
	if base == "" {
		base = tg.appURL(slug)
	}
	if path := str(receipt, "hosting_receipt.smoke.path"); path != "/ready" {
		t.Errorf("the smoke check used %q, not the declared health path /ready", path)
	}
	r := get(base + "/ready")
	if r.status != http.StatusOK || num(jsonBody(t, r), "port") != 9090 {
		t.Fatalf("GET /ready: %s (want the app listening on the declared port 9090)", r)
	}
}

// TestImageRegistryAndDeployTokens deploys a digest-pinned public image
// without a build (row 4), manages a private-registry credential on it
// without exposing the secret (row 15), and mints a per-app deploy token,
// deploys with it and revokes it (row 13).
func TestImageRegistryAndDeployTokens(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug := tg.app(t, "image")
	base := tg.appURL(slug)
	s := newStory(t)

	s.step("image_deploy", func(t *testing.T) {
		receipt := tg.deploy(t, slug, "--image", probeImage)
		if u := str(receipt, "app_url"); u != "" {
			base = strings.TrimRight(u, "/")
		}
		eventually(t, "the image serves its page", time.Minute, 2*time.Second, func() (bool, string) {
			r := get(base + "/")
			return r.status == http.StatusOK && strings.Contains(strings.ToLower(string(r.body)), "nginx"), r.String()
		})
	})
	s.check("registry_credentials", func(t *testing.T) {
		password := randomHex(t, 24)
		res := tg.execStdin(t, password+"\n", 2*time.Minute, "registry", "set", "--app", slug,
			"--registry", "ghcr.io", "--user", "e2e-journeys", "--password-stdin")
		if res.exit != 0 {
			t.Fatalf("registry set: %s", res.problem())
		}
		t.Cleanup(func() { tg.run(t, time.Minute, "registry", "rm", "--app", slug, "--registry", "ghcr.io") })
		listed := tg.ok(t, time.Minute, "registry", "list", "--app", slug)
		if !strings.Contains(string(listed.stdout), "ghcr.io") {
			t.Errorf("registry list does not show ghcr.io: %s", tail(string(listed.stdout), 300))
		}
		if strings.Contains(string(listed.stdout), password) {
			t.Errorf("registry list printed the password")
		}
		tg.ok(t, time.Minute, "registry", "rm", "--app", slug, "--registry", "ghcr.io")
		if again := tg.ok(t, time.Minute, "registry", "list", "--app", slug); strings.Contains(string(again.stdout), "e2e-journeys") {
			t.Errorf("the removed credential is still listed")
		}
	})
	s.check("deploy_tokens", func(t *testing.T) {
		key := tg.scopedKey(t, "tokens")
		if r := tg.apiCall(http.MethodGet, "/v1/apps/"+slug+"/deploy-tokens", nil, key); r.status != http.StatusOK {
			t.Fatalf("list deploy tokens: %s", r)
		}
		r := tg.apiCall(http.MethodPost, "/v1/apps/"+slug+"/deploy-tokens", map[string]string{"label": tg.slug("ci")}, key)
		if r.status == http.StatusForbidden || r.status == http.StatusUnauthorized {
			t.Skipf("minting a deploy token needs a recent step-up sign-in, which an API key cannot give (%s); "+
				"there is no CLI command for it either", r)
		}
		if r.status != http.StatusCreated && r.status != http.StatusOK {
			t.Fatalf("mint deploy token: %s", r)
		}
		doc := jsonBody(t, r)
		token := apiKeyRe.FindString(string(r.body))
		if token == "" {
			token = str(doc, "token_plaintext")
		}
		id := str(doc, "token.id")
		if token == "" || id == "" {
			t.Fatalf("deploy token response has no token or id: %s", tg.redact(compact(doc)))
		}
		t.Cleanup(func() { tg.apiCall(http.MethodDelete, "/v1/apps/"+slug+"/deploy-tokens/"+id, nil, key) })
		res := tg.exec(t, token, t.TempDir(), deployLimit+5*time.Minute, "deploy", "--name", slug, "--image", probeImage,
			"--wait", "--timeout", deployLimit.String(), "--yes", "--no-require-authn", "--reason", "e2e-journeys deploy token")
		if res.exit != 0 {
			t.Fatalf("deploy with the app's deploy token: %s", res.problem())
		}
		if other := tg.runAs(t, token, time.Minute, "apps"); other.exit == 0 {
			t.Errorf("a deploy token could list the account's apps")
		}
		if d := tg.apiCall(http.MethodDelete, "/v1/apps/"+slug+"/deploy-tokens/"+id, nil, key); d.status >= 300 {
			t.Fatalf("revoke deploy token: %s", d)
		}
		eventually(t, "revoked deploy token refused", time.Minute, 3*time.Second, func() (bool, string) {
			res := tg.exec(t, token, t.TempDir(), 2*time.Minute, "deploy", "--name", slug, "--image", probeImage,
				"--no-wait", "--yes", "--no-require-authn", "--reason", "e2e-journeys revoked token")
			return res.exit != 0, "revoked token still deploys"
		})
	})
}
