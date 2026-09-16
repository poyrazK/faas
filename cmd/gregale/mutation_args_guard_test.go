package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMutationLeavesRejectTrailingArgumentsBeforeNetwork(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = io.Discard, io.Discard, false
	t.Cleanup(func() {
		osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON
	})

	tests := []struct {
		name string
		call func() int
	}{
		{"traffic set", func() int {
			return cmdTrafficSet([]string{"--deployment", "00000000-0000-0000-0000-000000000000", "--percent", "50", "unexpected"})
		}},
		{"cron add", func() int {
			return cmdCrons([]string{"add", "--app", "demo", "--schedule", "*/5 * * * *", "unexpected"})
		}},
		{"domain add", func() int {
			return cmdDomains([]string{"add", "--domain", "demo.example.com", "--app", "demo", "unexpected"})
		}},
		{"deployment exclusion clear", func() int {
			return cmdDeploymentsExcludeClear([]string{"--slug", "demo", "--project-slug", "project", "unexpected"})
		}},
		{"registry set", func() int {
			return cmdRegistrySet([]string{"--app", "demo", "--registry", "ghcr.io", "--user", "user", "--password", "secret", "unexpected"})
		}},
		{"registry remove", func() int { return cmdRegistryRm([]string{"--app", "demo", "--registry", "ghcr.io", "unexpected"}) }},
		{"env push", func() int { return envPush([]string{"--app", "demo", "--from-stdin", "unexpected"}) }},
		{"app scale", func() int { return cmdAppScale("demo", []string{"--ram", "256", "unexpected"}) }},
		{"app update", func() int { return cmdApp([]string{"demo", "--min", "1", "unexpected"}) }},
		{"app security", func() int { return cmdAppSecurity("demo", []string{"--require-signed=true", "unexpected"}) }},
		{"static egress clear", func() int { return cmdAppStaticEgressIPClear("demo", []string{"unexpected"}) }},
		{"alert add", func() int {
			return cmdAlertAdd([]string{"--app", "demo", "--name", "latency", "--metric", "latency_p95_ms", "--comparison", "gt", "--threshold", "350", "--window-spec", "5m", "--webhook-url", "https://example.com/hook", "--webhook-secret", "secret", "unexpected"})
		}},
		{"edge rule create", func() int {
			return cmdEdgeRulesCreate([]string{"--app", "demo", "--kind", "maintenance", "--match-host", "demo.example.com", "unexpected"})
		}},
		{"workflow run", func() int { return cmdWorkflowsRun([]string{"nightly", "--app", "demo", "unexpected"}) }},
		{"job add", func() int {
			return cmdJobsAdd([]string{"demo-job", "--image", "example.com/demo:latest", "unexpected"})
		}},
		{"job update", func() int { return cmdJobsUpdate([]string{"demo-job", "--pause", "unexpected"}) }},
		{"job run", func() int { return cmdJobsRun([]string{"demo-job", "--tasks", "1", "unexpected"}) }},
		{"mirror create", func() int {
			return cmdMirrorCreate([]string{"--app", "demo", "--source", "source-id", "--mirror", "mirror-id", "unexpected"})
		}},
		{"mirror update", func() int {
			return cmdMirrorUpdate([]string{"--app", "demo", "--id", "mirror-id", "--disable", "unexpected"})
		}},
		{"mirror remove", func() int { return cmdMirrorRm([]string{"--app", "demo", "--id", "mirror-id", "unexpected"}) }},
		{"webhook add", func() int {
			return cmdWebhooksAdd([]string{"--app", "demo", "--target-url", "https://example.com/hook", "unexpected"})
		}},
		{"org create", func() int { return cmdOrgsCreate([]string{"--slug", "demo-org", "--name", "Demo", "unexpected"}) }},
		{"org member invite", func() int {
			return cmdOrgsMembersInvite([]string{"--org", "demo-org", "--email", "user@example.com", "unexpected"})
		}},
		{"org member role", func() int {
			return cmdOrgsMembersChangeRole([]string{"--org", "demo-org", "--user", "user-id", "--role", "viewer", "unexpected"})
		}},
		{"org member remove", func() int { return cmdOrgsMembersRm([]string{"--org", "demo-org", "--user", "user-id", "unexpected"}) }},
		{"org invitation revoke", func() int {
			return cmdOrgsInvitationsRevoke([]string{"--org", "demo-org", "--invitation", "invitation-id", "unexpected"})
		}},
		{"org ownership transfer", func() int {
			return cmdOrgsTransferOwnership([]string{"--org", "demo-org", "--to", "user-id", "unexpected"})
		}},
		{"org key add", func() int {
			return cmdOrgsKeysAdd([]string{"--org", "demo-org", "--label", "automation", "unexpected"})
		}},
		{"org update", func() int { return cmdOrgsUpdate([]string{"--org", "demo-org", "--name", "Updated", "unexpected"}) }},
		{"tenant surface add", func() int {
			return cmdTenantSurfacesAdd([]string{"--app", "demo", "--name", "customers", "unexpected"})
		}},
		{"tenant hostname add", func() int {
			return cmdTenantSurfacesHostnameAdd([]string{"--app", "demo", "--surface", "surface-id", "--hostname", "tenant.example.com", "unexpected"})
		}},
		{"github webhook secret set", func() int {
			return githubWebhookSecretSet([]string{"--installation-id", "1", "--secret", strings.Repeat("a", 32), "unexpected"})
		}},
		{"rollout recover", func() int {
			return cmdRolloutsRecover([]string{"demo", "--action", "advance", "unexpected"})
		}},
		{"mfa confirm", func() int { return cmdMfaConfirm([]string{"--code", "123456", "unexpected"}) }},
		{"mfa verify", func() int { return cmdMfaVerify([]string{"--code", "123456", "unexpected"}) }},
		{"mfa recover", func() int { return cmdMfaRecover([]string{"--code", "AAAAAAAAAA", "unexpected"}) }},
		{"mfa disable", func() int { return cmdMfaDisable([]string{"--password", "password", "unexpected"}) }},
		{"obsolete deployments clear", func() int { return cmdDeploysClearObsolete([]string{"--app", "demo", "--force", "unexpected"}) }},
		{"delayed task add", func() int {
			return cmdDelayedTaskAdd([]string{"--app", "demo", "--scheduled-at", "2035-01-01T00:00:00Z", "unexpected"})
		}},
		{"openapi remove", func() int { return cmdOpenapiRemove([]string{"demo", "unexpected"}) }},
		{"account delete", func() int { return cmdAccountDelete([]string{"-q", "unexpected"}) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := requests.Load()
			if code := test.call(); code == 0 {
				t.Fatal("command accepted an unexpected positional argument")
			}
			if got := requests.Load(); got != before {
				t.Fatalf("command sent %d request(s) before rejecting its arguments", got-before)
			}
		})
	}
}

func TestUnexpectedMutationArgsReturnStableJSONValidation(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	oldErr, oldJSON := osStderr, jsonOutput
	jsonOutput = true
	t.Cleanup(func() { osStderr, jsonOutput = oldErr, oldJSON })

	tests := []struct {
		name string
		call func() int
	}{
		{"traffic", func() int {
			return cmdTrafficSet([]string{"--deployment", "00000000-0000-0000-0000-000000000000", "--percent", "50", "unexpected"})
		}},
		{"cron", func() int {
			return cmdCrons([]string{"add", "--app", "demo", "--schedule", "*/5 * * * *", "unexpected"})
		}},
		{"deployment exclusion", func() int {
			return cmdDeploymentsExcludeClear([]string{"--slug", "demo", "--project-slug", "project", "unexpected"})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			osStderr = &stderr
			before := requests.Load()
			if code := test.call(); code == 0 {
				t.Fatal("command accepted an unexpected positional argument")
			}
			if got := requests.Load(); got != before {
				t.Fatalf("command sent %d request(s) before rejecting its arguments", got-before)
			}
			var problem api.Problem
			if err := json.Unmarshal(stderr.Bytes(), &problem); err != nil {
				t.Fatalf("stderr is not a JSON problem: %v; raw=%q", err, stderr.String())
			}
			if problem.Code != api.CodeValidation || problem.Status != http.StatusBadRequest {
				t.Fatalf("problem = %#v, want validation_failed/400", problem)
			}
			if !strings.Contains(problem.Detail, "unexpected") {
				t.Fatalf("detail %q does not identify the unconsumed argument", problem.Detail)
			}
		})
	}
}

// This tripwire inspects every non-test CLI handler. A handler that owns a
// FlagSet and invokes a mutating client method must prove exact positional
// consumption before the first write. Dynamic tests above cover the customer
// reproductions; this test closes the same class for newly added commands.
func TestMutatingFlagHandlersGuardPositionalsBeforeClientCalls(t *testing.T) {
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg := packages["main"]
	if pkg == nil {
		t.Fatal("main package not found")
	}

	for _, file := range pkg.Files {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// CORS intentionally consumes every positional after the slug as
			// another origin; it has no unconsumed trailing-token state.
			if fn.Name.Name == "cmdCorsAllow" {
				continue
			}
			flagSet, mutation, guard := token.NoPos, token.NoPos, token.NoPos
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.CallExpr:
					if name := calledIdent(n.Fun); name == "newFlagSet" || name == "newOpenapiFlagSet" || name == "triggerFlagSet" {
						flagSet = earliestPosition(flagSet, n.Pos())
					}
					if calledIdent(n.Fun) == "rejectUnexpectedFlagArgs" {
						guard = earliestPosition(guard, n.Pos())
					}
					if selector, ok := n.Fun.(*ast.SelectorExpr); ok && isMutationMethod(selector.Sel.Name) {
						if receiver, ok := selector.X.(*ast.Ident); ok && !nonClientReceiver(receiver.Name) {
							mutation = earliestPosition(mutation, n.Pos())
						}
					}
				case *ast.BinaryExpr:
					if n.Op == token.NEQ && isExactArgumentCount(n.X, n.Y) {
						guard = earliestPosition(guard, n.Pos())
					}
				}
				return true
			})
			if flagSet != token.NoPos && mutation != token.NoPos && (guard == token.NoPos || guard > mutation) {
				position := fset.Position(fn.Pos())
				t.Errorf("%s (%s:%d) mutates through a FlagSet without exact positional validation before the client call", fn.Name.Name, position.Filename, position.Line)
			}
		}
	}
}

func calledIdent(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func earliestPosition(current, candidate token.Pos) token.Pos {
	if current == token.NoPos || candidate < current {
		return candidate
	}
	return current
}

func isExactArgumentCount(left, right ast.Expr) bool {
	count, ok := left.(*ast.CallExpr)
	if !ok {
		return false
	}
	integer, ok := right.(*ast.BasicLit)
	if !ok || integer.Kind != token.INT {
		return false
	}
	if _, err := strconv.Atoi(integer.Value); err != nil {
		return false
	}
	if calledIdent(count.Fun) == "len" && len(count.Args) == 1 {
		identifier, ok := count.Args[0].(*ast.Ident)
		if ok {
			name := strings.ToLower(identifier.Name)
			return name == "args" || name == "rest" || strings.Contains(name, "pos")
		}
		selectorCall, ok := count.Args[0].(*ast.CallExpr)
		return ok && calledIdent(selectorCall.Fun) == "Args"
	}
	selector, ok := count.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "NArg" && len(count.Args) == 0
}

func isMutationMethod(name string) bool {
	for _, prefix := range []string{
		"Accept", "Acknowledge", "Add", "Apply", "Bind", "Cancel", "Change", "Clear", "Confirm", "Create", "Delete", "Deploy", "Disable", "Disconnect", "Enable", "Invite", "Issue", "Mint", "Park", "Patch", "Post", "Purge", "Put", "Recover", "Remove", "Restore", "Retry", "Revoke", "Rollback", "Rotate", "Run", "Send", "Set", "Submit", "Transfer", "Trigger", "Update", "Wake",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func nonClientReceiver(name string) bool {
	switch name {
	case "api", "bytes", "context", "errors", "flag", "fmt", "hex", "http", "io", "json", "os", "rand", "sort", "strconv", "strings", "sync", "term", "time", "url":
		return true
	default:
		return false
	}
}
