package operationbindings

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixture() Contract {
	return Contract{App: "orders", SHA256: strings.Repeat("a", 64), Operations: []Operation{
		{Name: "fulfill", Milestones: []string{"paid", "order-done"}, Workflows: []Workflow{{Name: "order-flow", Version: 2, States: []string{"pending", "done"}, TerminalStates: []string{"done"}, Transitions: []Transition{{From: "pending", To: "done", RequiredMilestones: []string{"paid", "order-done"}}}}}},
		{Name: "inspect", Workflows: []Workflow{{Name: "order-flow", Version: 2, States: []string{"done", "pending"}, TerminalStates: []string{"done"}, TransitionsDeclared: true}}},
		{Name: "observe", Workflows: []Workflow{{Name: "monitor", Version: 1, States: []string{"ready", "halted"}}}},
	}}
}

func TestDeterministicAndOperationScoped(t *testing.T) {
	for _, language := range []string{"javascript", "typescript", "python", "go"} {
		t.Run(language, func(t *testing.T) {
			first, err := Render(fixture(), language, "workflowbindings")
			if err != nil {
				t.Fatal(err)
			}
			contract := fixture()
			contract.Operations[0], contract.Operations[2] = contract.Operations[2], contract.Operations[0]
			second, err := Render(contract, language, "workflowbindings")
			if err != nil || !bytes.Equal(first, second) {
				t.Fatalf("output depends on input order: %v", err)
			}
			if strings.Contains(string(first), "report_order_flow__inspect") || strings.Contains(string(first), "Report_order_flow__inspect") || strings.Contains(string(first), "transition_order_flow__inspect") {
				t.Fatal("generated a helper for an Operation with no declared edge")
			}
		})
	}
}

func TestRejectIdentifierCollisions(t *testing.T) {
	contract := fixture()
	contract.Operations = []Operation{
		{Name: "b--c", Workflows: []Workflow{{Name: "a", Version: 1, States: []string{"pending", "done"}, Transitions: []Transition{{From: "pending", To: "done"}}}}},
		{Name: "c", Workflows: []Workflow{{Name: "a--b", Version: 1, States: []string{"pending", "done"}, Transitions: []Transition{{From: "pending", To: "done"}}}}},
	}
	if _, err := Render(contract, "javascript", ""); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("ambiguous exports accepted: %v", err)
	}
	for _, language := range []string{"rust", "go"} {
		if _, err := Render(fixture(), language, "package"); err == nil {
			t.Fatalf("invalid language/package accepted: %s", language)
		}
	}
}

func TestGeneratedTypeScriptRejectsContractMistakes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	compiler, err := filepath.Abs("../../sdk/node/node_modules/typescript/bin/tsc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(compiler); err != nil {
		t.Skip("install sdk/node dependencies for TypeScript type checks")
	}
	body, err := Render(fixture(), "typescript", "")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bindings.ts"), body, 0600); err != nil {
		t.Fatal(err)
	}
	runner := `import * as b from './bindings';
declare const tx: b.WorkflowTransaction;
b.transition_order_flow__fulfill__pending__done(tx, 'run', b.State_order_flow__pending, {}, {});
// @ts-expect-error Required milestone payload cannot be omitted.
b.transition_order_flow__fulfill__pending__done(tx, 'run', b.State_order_flow__pending, {});
// @ts-expect-error Undeclared source states cannot enter application code.
const state: b.State_order_flow = 'invented';
// @ts-expect-error This Operation has no declared transition.
b.transition_order_flow__inspect__pending__done(tx, 'run', 'pending', {}, {});
`
	if err := os.WriteFile(filepath.Join(dir, "runner.ts"), []byte(runner), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, compiler, "--strict", "--noEmit", "--target", "ES2022", "--skipLibCheck", "runner.ts")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated TypeScript contract checks failed: %v\n%s", err, output)
	}
}

func TestGeneratedCodeBehavior(t *testing.T) {
	tests := []struct {
		language, tool, extension, runner string
		args                              []string
	}{
		{"javascript", "node", ".mjs", `import assert from 'node:assert/strict';
import * as b from './bindings.mjs';
const calls = [];
const tx = {milestone: (...v) => calls.push(['milestone', ...v]), workflowTransition: (...v) => calls.push(['transition', ...v]), workflowState: (...v) => calls.push(['state', ...v])};
const apply = b.transition_order_flow__fulfill__pending__done;
apply(tx, 'run', b.State_order_flow__pending, {done: 1}, {paid: 1});
assert.deepEqual(calls, [['milestone', 'order-done', {done: 1}], ['milestone', 'paid', {paid: 1}], ['transition', 'order-flow', 'run', 'pending', 'done']]);
calls.length = 0;
assert.throws(() => apply(tx, 'run', 'done', {}, {}));
assert.throws(() => apply(tx, 'run', 'pending', {}));
assert.equal(calls.length, 0);
const failure = new Error('payload schema');
assert.throws(() => apply({...tx, milestone: () => {throw failure;}}, 'run', 'pending', {}, {}), e => e === failure);
assert.equal(calls.length, 0);
assert.throws(() => b.report_monitor__observe(tx, 'run', 'unknown'));
b.report_monitor__observe(tx, 'run', 'ready');
assert.deepEqual(calls, [['state', 'monitor', 'run', 'ready']]);
`, []string{"runner.mjs"}},
		{"python", "python3", ".py", `import bindings as b
calls = []
class Tx:
    def milestone(self, *v): calls.append(('milestone', *v))
    def workflow_transition(self, *v): calls.append(('transition', *v))
    def workflow_state(self, *v): calls.append(('state', *v))
tx = Tx()
apply = b.transition_order_flow__fulfill__pending__done
apply(tx, 'run', b.State_order_flow__pending, {'done': 1}, {'paid': 1})
assert calls == [('milestone', 'order-done', {'done': 1}), ('milestone', 'paid', {'paid': 1}), ('transition', 'order-flow', 'run', 'pending', 'done')]
calls.clear()
for args, error in [((tx, 'run', 'done', {}, {}), ValueError), ((tx, 'run', 'pending', {}), TypeError)]:
    try: apply(*args)
    except error: pass
    else: raise AssertionError('invalid call accepted')
assert not calls
failure = RuntimeError('schema')
class FailingTx(Tx):
    def milestone(self, *v): raise failure
try: apply(FailingTx(), 'run', 'pending', {}, {})
except RuntimeError as error: assert error is failure
else: raise AssertionError('milestone failure swallowed')
assert not calls
try: b.report_monitor__observe(tx, 'run', 'unknown')
except ValueError: pass
else: raise AssertionError('undeclared state accepted')
b.report_monitor__observe(tx, 'run', 'ready')
assert calls == [('state', 'monitor', 'run', 'ready')]
`, []string{"runner.py"}},
		{"go", "go", ".go", `package workflowbindings
import ("testing"; "errors"; "reflect")
type tx struct { calls []string; failure error }
func (x *tx) Milestone(name string, _ any) error { if x.failure != nil { return x.failure }; x.calls=append(x.calls, name); return nil }
func (x *tx) WorkflowTransition(w, i, f, s string) error { x.calls=append(x.calls, w+"/"+i+"/"+f+"/"+s); return nil }
func (x *tx) WorkflowState(w, i, s string) error { x.calls=append(x.calls,w+"/"+i+"/"+s); return nil }
func TestBehavior(t *testing.T) {
x:= &tx{}
if err:=Transition_order_flow__fulfill__pending__done(x,"run",State_order_flow__pending,1,2); err!=nil {t.Fatal(err)}
if !reflect.DeepEqual(x.calls,[]string{"order-done","paid","order-flow/run/pending/done"}) {t.Fatal(x.calls)}
x.calls=nil
if err:=Transition_order_flow__fulfill__pending__done(x,"run",State_order_flow__done,1,2);err==nil||len(x.calls)!=0 {t.Fatal("source check failed")}
x.failure=errors.New("schema")
if err:=Transition_order_flow__fulfill__pending__done(x,"run",State_order_flow__pending,1,2);err!=x.failure||len(x.calls)!=0 {t.Fatal("failure swallowed")}
x.failure=nil
if err:=Report_monitor__observe(x,"run",State_monitor("unknown"));err==nil {t.Fatal("invalid snapshot")}
if err:=Report_monitor__observe(x,"run",State_monitor__ready);err!=nil {t.Fatal(err)}
}
`, []string{"test", "./..."}},
	}
	for _, test := range tests {
		t.Run(test.language, func(t *testing.T) {
			tool, err := exec.LookPath(test.tool)
			if err != nil {
				t.Skipf("%s unavailable", test.tool)
			}
			body, err := Render(fixture(), test.language, "workflowbindings")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("bindings"+test.extension, string(body))
			runner := "runner" + test.extension
			if test.language == "go" {
				runner = "runner_test.go"
				write("go.mod", "module generatedbindings\n\ngo 1.23\n")
			}
			write(runner, test.runner)
			command := exec.Command(tool, test.args...)
			command.Dir = dir
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("generated %s failed: %v\n%s", test.language, err, output)
			}
		})
	}
}
