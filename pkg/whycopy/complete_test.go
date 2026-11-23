// adr: 20261123230000

package whycopy_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

// The voting-app worker's startup crash (2026-11-23 agent eval) printed
// `gregale logs <slug>` although the deploy knew the app. With the app and
// deployment known, its fix names them and its commands run as written.
func TestCompleteFillsTheStartupCrashFix(t *testing.T) {
	p := whycopy.Decorate(api.NewProblem(422, api.CodeAppStartupCrash, "workload stopped during startup", "boom"), api.CodeAppStartupCrash, nil)
	if strings.Contains(p.Fix, "voting-demo-worker") {
		t.Fatal("test setup: the catalog fix already names the app")
	}
	for _, c := range p.FixCommands {
		if strings.Contains(c, "<") {
			t.Fatalf("FixCommands = %q holds a placeholder before the app is known", p.FixCommands)
		}
	}
	whycopy.Complete(p, whycopy.Values{App: "voting-demo-worker", Deployment: "dep-1"})
	if strings.Contains(p.Fix, "<slug>") || !strings.Contains(p.Fix, "gregale logs voting-demo-worker --since 30m") {
		t.Fatalf("fix = %q; want the app filled in", p.Fix)
	}
	for _, want := range []string{"gregale logs voting-demo-worker --since 30m", "gregale secrets list --app voting-demo-worker"} {
		if !slices.Contains(p.FixCommands, want) {
			t.Errorf("FixCommands = %q; want %q", p.FixCommands, want)
		}
	}
	if p.Retryable == nil || *p.Retryable {
		t.Fatalf("a startup crash must not be retryable as is, got %v", p.Retryable)
	}
}

func TestCommandsLeaveOutTemplates(t *testing.T) {
	fix := "• run `gregale deploys status dep-1`\n• set it with `gregale env set --app api ENV_VAR_NAME=<value>`\n" +
		"• or `gregale release resolve dep-1 --app api --note TEXT`, `gregale deploy --image=...`, `gregale app api --ram %d`\n• again: `gregale deploys status dep-1`"
	if got := whycopy.Commands(fix); !slices.Equal(got, []string{"gregale deploys status dep-1"}) {
		t.Fatalf("Commands = %q; want only the runnable command, once", got)
	}
}

// The CLI completes every problem it prints with no values; commands an API
// sent stay as sent.
func TestCompleteKeepsSentCommands(t *testing.T) {
	sent := []string{"gregale logs api --deployment dep-1"}
	p := &api.Problem{Status: 422, Fix: "• read `gregale logs api`", FixCommands: slices.Clone(sent)}
	whycopy.Complete(p, whycopy.Values{})
	if !slices.Equal(p.FixCommands, sent) {
		t.Fatalf("FixCommands = %q; want the sent %q", p.FixCommands, sent)
	}
	p = &api.Problem{Status: 422, Fix: "• read `gregale logs <slug>`", FixCommands: []string{"stale"}}
	whycopy.Complete(p, whycopy.Values{App: "api"})
	if !slices.Equal(p.FixCommands, []string{"gregale logs api"}) {
		t.Fatalf("FixCommands = %q; want recomputed from the filled fix", p.FixCommands)
	}
}

func TestRetryable(t *testing.T) {
	seconds := int64(5)
	for _, tc := range []struct {
		p    api.Problem
		want *bool
	}{
		{api.Problem{Status: 429}, ptr(true)},
		{api.Problem{Status: 503}, ptr(true)},
		{api.Problem{Status: 408}, ptr(true)},
		{api.Problem{Status: 425}, ptr(true)},
		{api.Problem{Status: 409, RetryAfterSeconds: &seconds}, ptr(true)},
		{api.Problem{Status: 409}, nil},
		{api.Problem{Status: 423}, nil},
		{api.Problem{Status: 422}, ptr(false)},
		{api.Problem{Status: 500}, nil},
	} {
		got := whycopy.Retryable(&tc.p)
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("Retryable(status %d) = %v, want %v", tc.p.Status, got, tc.want)
		}
	}
}

func ptr(b bool) *bool { return &b }
