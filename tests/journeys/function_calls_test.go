package journeys

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestFunctionCalls deploys a two-function project (ADR-20261111150000) and
// drives the typed-client wire contract from the caller: the deploy binds the
// siblings and names them in GREGALE_FUNCTIONS, a call over the private
// binding returns the callee's output, a body that breaks the callee's input
// schema is refused with 422 function_input_invalid before its handler runs,
// and a send is enqueued as a durable async invocation that completes.
func TestFunctionCalls(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	project := tg.slug("fncalls")
	web, users := project+"-web", project+"-users"
	for _, slug := range []string{web, users} {
		tg.ownApp(t, slug)
	}
	src := fixtureSource(t, "function-calls")
	s := newStory(t)

	if !s.step("deploy_project", func(t *testing.T) {
		tg.deploySlots <- struct{}{}
		defer func() { <-tg.deploySlots }()
		res := tg.exec(t, tg.token, t.TempDir(), 2*deployLimit+5*time.Minute, "deploy", "--path", src, "--name", project,
			"--wait", "--timeout", deployLimit.String(), "--yes", "--no-require-authn", "--reason", "e2e-journeys run "+tg.runID)
		if res.exit != 0 {
			t.Fatalf("deploy %s failed (exit %d): %s\n%s", project, res.exit, tg.redact(res.problem()), tg.redact(tail(string(res.stdout)+string(res.stderr), 2000)))
		}
	}) {
		return
	}
	base := tg.appURL(web)

	s.check("callables_set", func(t *testing.T) {
		eventually(t, "web names users in GREGALE_FUNCTIONS", 2*time.Minute, 2*time.Second, func() (bool, string) {
			r := get(base + "/functions")
			if r.status != http.StatusOK {
				return false, r.String()
			}
			doc := jsonBody(t, r)
			return str(doc, "functions.users") == users, compact(doc)
		})
	})
	s.check("call_returns_output", func(t *testing.T) {
		eventually(t, "web calls users over the binding", 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/call?email=a@example.com")
			if r.status != http.StatusOK {
				return false, r.String()
			}
			doc := jsonBody(t, r)
			return num(doc, "status") == http.StatusOK && str(doc, "body.hello") == "a@example.com" &&
				str(doc, "body.probe") == "gregale-e2e-users", compact(doc)
		})
	})
	s.check("bad_input_refused", func(t *testing.T) {
		doc := jsonBody(t, get(base+"/bad"))
		if num(doc, "status") != http.StatusUnprocessableEntity || str(doc, "body.code") != "function_input_invalid" ||
			!strings.Contains(str(doc, "body.detail"), "input.email") {
			t.Fatalf("a call breaking users' input schema = %s, want 422 function_input_invalid naming input.email", compact(doc))
		}
	})
	s.check("send_enqueues_invocation", func(t *testing.T) {
		doc := jsonBody(t, get(base+"/send?email=sent@example.com"))
		if num(doc, "status") == http.StatusForbidden && strings.Contains(compact(doc), "plan") {
			t.Skipf("the account's plan has no async invocations: %s", compact(doc))
		}
		id := str(doc, "body.id")
		if num(doc, "status") != http.StatusAccepted || id == "" {
			t.Fatalf("send = %s, want 202 with an invocation id", compact(doc))
		}
		eventually(t, "the sent invocation completes", 3*time.Minute, 5*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "invocation", "get", id)
			if res.exit != 0 {
				return false, res.problem()
			}
			inv := object(t, res.stdout)
			if state := str(inv, "state"); state == "failed" || state == "cancelled" {
				t.Fatalf("sent invocation ended %s: %s", state, compact(inv))
			}
			return str(inv, "state") == "completed", compact(inv)
		})
	})
}

// ownApp holds an app slot for an app a project deploy creates under its own
// slug, and deletes the app when the journey ends.
func (tg *target) ownApp(t *testing.T, slug string) {
	t.Helper()
	tg.acquireAppSlot(t)
	t.Cleanup(func() {
		defer func() { <-tg.appSlots }()
		if tg.keep && t.Failed() {
			t.Logf("keeping %s for debugging (GREGALE_E2E_KEEP=1)", slug)
			return
		}
		res := tg.run(t, 2*time.Minute, "app", "delete", slug, "--yes")
		if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: cleanup could not delete %s (%s); the next run's sweeper removes it", slug, res.problem())
		}
	})
}
