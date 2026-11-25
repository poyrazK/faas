package main

// adr: 20261027090013 step 13 — the session's environment reaches every
// later command, a command's own env wins over it, and replacing it with an
// empty one clears it. The answer names the variables, never their values.

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

func TestSandboxSessionEnv(t *testing.T) {
	f := newSandboxFixture(t)
	setEnv := func(env map[string]string) []string {
		var res sandboxproto.SessionEnvResult
		f.call(t, sandboxproto.Request{Op: sandboxproto.OpSessionEnv, Env: env}, nil, nil).decode(t, &res)
		return res.Names
	}
	printed := func(env map[string]string) string {
		req := execReq("sh", "-c", `printf '%s|%s' "$TOKEN" "$MODE"`)
		req.Env = env
		r := f.call(t, req, nil, nil)
		r.decode(t, &sandboxproto.ExecResult{})
		return r.stdout.String()
	}

	if names := setEnv(map[string]string{"TOKEN": "s3cret", "MODE": "session"}); !slices.Equal(names, []string{"MODE", "TOKEN"}) {
		t.Fatalf("names %v", names)
	}
	if got := printed(nil); got != "s3cret|session" {
		t.Fatalf("a command without env saw %q", got)
	}
	if got := printed(map[string]string{"MODE": "command"}); got != "s3cret|command" {
		t.Fatalf("a command's own env saw %q", got)
	}
	if names := setEnv(nil); names == nil || len(names) != 0 {
		t.Fatalf("clearing answered %v", names)
	}
	if got := printed(nil); got != "|" {
		t.Fatalf("after clearing, a command saw %q", got)
	}
	bad := sandboxproto.Request{Op: sandboxproto.OpSessionEnv, Env: map[string]string{"A B": "x"}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "not a valid variable") {
		t.Fatalf("a bad name: %v", err)
	}
}
