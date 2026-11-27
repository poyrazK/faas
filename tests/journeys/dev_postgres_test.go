package journeys

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDevPostgres is `gregale dev --postgres`: the developer environment gets
// its own database, its release command creates the schema through
// MIGRATION_DATABASE_URL (a read_write login cannot), the serving app writes
// and reads rows through DATABASE_URL, and `--stop` removes the environment
// and its database. Before 2026-11-21 developer databases had no migration
// credential, so an app could not create a single table.
func TestDevPostgres(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	s := newStory(t)
	name := tg.slug("devpg")
	src := postgresProbeSource(t, "create")

	port := freePort(t)
	proxy := "http://localhost:" + strconv.Itoa(port)
	stop := tg.startDev(t, src, "dev", "--path", src, "--name", name, "--postgres", "--no-logs", "--ttl", "1h", "--proxy-port", strconv.Itoa(port))
	t.Cleanup(func() {
		stop()
		tg.run(t, 3*time.Minute, "dev", "--path", src, "--name", name, "--stop", "--yes")
	})

	var devURL string
	if !s.step("first_sync", func(t *testing.T) {
		eventually(t, "the developer environment serving", 12*time.Minute, 10*time.Second, func() (bool, string) {
			devURL = tg.devSessionURL(t, name)
			if devURL == "" {
				return false, "not listed by gregale dev list yet"
			}
			r := tg.devSend(http.MethodGet, devURL, proxy, "/healthz")
			return r.status == http.StatusOK, r.String()
		})
	}) {
		return
	}
	if !s.step("rows", func(t *testing.T) {
		// The release command made the table; the serving app holds only
		// DATABASE_URL, so a write that lands proves both credentials.
		var last httpResult
		eventually(t, "a row written through DATABASE_URL", 5*time.Minute, 5*time.Second, func() (bool, string) {
			last = tg.devSend(http.MethodPost, devURL, proxy, "/notes?env=DATABASE_URL&body=dev-row")
			return last.status == http.StatusOK, last.String()
		})
		if str(jsonBody(t, last), "body") != "dev-row" {
			t.Fatalf("write answered %s", last.String())
		}
		read := tg.devSend(http.MethodGet, devURL, proxy, "/notes?env=DATABASE_URL")
		list, _ := field(jsonBody(t, read), "bodies").([]any)
		if len(list) != 1 || list[0] != "dev-row" {
			t.Fatalf("rows = %v, want [dev-row]", list)
		}
	}) {
		return
	}
	s.step("stop", func(t *testing.T) {
		database := devAppSlug(t, devURL)
		stop()
		tg.ok(t, 3*time.Minute, "dev", "--path", src, "--name", name, "--stop", "--yes")
		eventually(t, "the environment and its database gone", 5*time.Minute, 10*time.Second, func() (bool, string) {
			if tg.devSessionURL(t, name) != "" {
				return false, "still listed by gregale dev list"
			}
			for _, item := range items(object(t, tg.ok(t, time.Minute, "postgres", "list").stdout), "items") {
				if str(item, "name") == database && str(item, "state") != "deleted" {
					return false, "database " + database + " is " + str(item, "state")
				}
			}
			return true, ""
		})
	})
}

// devSend is devGet for any method: directly with the journey's API key,
// otherwise through the CLI's localhost proxy.
func (tg *target) devSend(method, devURL, proxy, path string) httpResult {
	if tg.token != "" {
		return fetch(method, devURL+path, map[string]string{"Authorization": "Bearer " + tg.token})
	}
	if r := fetch(method, proxy+path, nil); r.err == nil {
		return r
	}
	return fetch(method, devURL+path, nil)
}

// devAppSlug is the developer app's slug, which also names its database: the
// first label of the environment's hostname.
func devAppSlug(t *testing.T, devURL string) string {
	t.Helper()
	u, err := url.Parse(devURL)
	if err != nil || u.Hostname() == "" {
		t.Fatalf("developer environment URL %q: %v", devURL, err)
	}
	return strings.SplitN(u.Hostname(), ".", 2)[0]
}
