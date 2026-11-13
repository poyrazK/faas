package journeys

import (
	"net/http"
	"testing"
	"time"
)

// TestWorkflowFunctions deploys a project with a workflow function
// (ADR-20261113120824) and drives it from a sibling: web starts orders with
// the guest workflow-runs route (an idempotent retry returns the same run),
// orders calls users with wf.call and parks on an event, web signals the run
// and its long-poll returns the output with the sibling's answer and the
// event's payload.
func TestWorkflowFunctions(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	project := tg.slug("wffns")
	web := project + "-web"
	for _, name := range []string{"web", "orders", "users"} {
		tg.ownApp(t, project+"-"+name)
	}
	src := fixtureSource(t, "workflow-functions")
	s := newStory(t)

	if !s.step("deploy_project", func(t *testing.T) {
		tg.deploySlots <- struct{}{}
		defer func() { <-tg.deploySlots }()
		res := tg.exec(t, tg.token, t.TempDir(), 3*deployLimit+5*time.Minute, "deploy", "--path", src, "--name", project,
			"--wait", "--timeout", deployLimit.String(), "--yes", "--no-require-authn", "--reason", "e2e-journeys run "+tg.runID)
		if res.exit != 0 {
			t.Fatalf("deploy %s failed (exit %d): %s\n%s", project, res.exit, tg.redact(res.problem()), tg.redact(tail(string(res.stdout)+string(res.stderr), 2000)))
		}
	}) {
		return
	}
	base := tg.appURL(web)
	key := "e2e-" + tg.runID
	var runID string

	if !s.step("start_run", func(t *testing.T) {
		eventually(t, "web starts an orders run", 3*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/start?key=" + key)
			if r.status != http.StatusOK {
				return false, r.String()
			}
			doc := jsonBody(t, r)
			runID = str(doc, "body.id")
			return num(doc, "status") == http.StatusCreated && runID != "", compact(doc)
		})
	}) {
		return
	}
	s.check("idempotent_start", func(t *testing.T) {
		doc := jsonBody(t, get(base+"/start?key="+key))
		if num(doc, "status") != http.StatusCreated || str(doc, "body.id") != runID {
			t.Fatalf("retried start = %s, want run %s again", compact(doc), runID)
		}
	})
	s.check("signal_and_wait", func(t *testing.T) {
		eventually(t, "web signals the parked run", 2*time.Minute, 3*time.Second, func() (bool, string) {
			doc := jsonBody(t, get(base+"/signal?id="+runID))
			return num(doc, "status") == http.StatusAccepted, compact(doc)
		})
		eventually(t, "the run finishes with the sibling's answer and the event", 3*time.Minute, time.Second, func() (bool, string) {
			doc := jsonBody(t, get(base+"/wait?id="+runID))
			return str(doc, "body.status") == "succeeded" && str(doc, "body.output.user.hello") == "a@example.com" &&
				str(doc, "body.output.user.probe") == "gregale-e2e-users", compact(doc)
		})
	})
}
