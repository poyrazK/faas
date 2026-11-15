package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestListFlagRuleBooleanMatrix(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, output := range []bool{false, true} {
				for _, mode := range []bool{false, true} {
					state := main.ListFlagRuleState{All: all, Stream: stream, Output: output, JSON: mode}
					for _, test := range []struct {
						id   string
						fail bool
						text string
					}{
						{main.ListOutputRequiresAll, output && !all, "--output requires --all"},
						{main.ListStreamRequiresAllJSON, stream && !output && (!all || !mode), "--stream requires --json and --all"},
					} {
						err := main.ListFlagRuleViolation(test.id, state, "build list")
						if (err != nil) != test.fail {
							t.Fatalf("%s %+v: %v", test.id, state, err)
						}
						if err != nil && err.Error() != test.text {
							t.Fatalf("human error changed: %q", err.Error())
						}
					}
				}
			}
		}
	}
	if main.ListFlagRuleViolation("unregistered", main.ListFlagRuleState{Output: true}, "private") != nil {
		t.Fatal("unregistered rule executed")
	}
}

func TestListFlagRuleVisibilityAndOwnership(t *testing.T) {
	complete := main.VisibleListFlagRules([]string{"all", "stream", "output"})
	if !reflect.DeepEqual(complete, main.ListFlagRules()) {
		t.Fatal("discovery and validation rules drifted")
	}
	for _, flags := range [][]string{nil, {"all"}, {"all", "stream"}, {"all", "output"}} {
		rules := main.VisibleListFlagRules(flags)
		for _, rule := range rules {
			for _, condition := range append(append([]main.CLIFlagRuleCondition{}, rule.When...), rule.Requires...) {
				if condition.Test == "effective" {
					if condition.Flag != "json" {
						t.Fatal(condition)
					}
					continue
				}
				found := false
				for _, name := range flags {
					found = found || name == condition.Flag
				}
				if !found {
					t.Fatalf("hidden flag: %s %v", condition.Flag, flags)
				}
			}
		}
	}
	complete[0].When[0].Flag = "private-input"
	complete[1].Requires[0].Flag = "private-input"
	data, err := json.Marshal(main.ListFlagRules())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "message") || !strings.Contains(string(data), `"value":false`) {
		t.Fatalf("rule metadata: %s", data)
	}
}

type flagRuleShortWriter struct{}

func (flagRuleShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

type flagRuleBrokenWriter struct{}

func (flagRuleBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestListFlagRuleProblemPreservesEnvelope(t *testing.T) {
	original := []byte(`{"code":"validation_failed","category":"invalid_request","exit_code":1,"request_id":"fixture","detail":"same","observed":9007199254740993,"extra":{"n":9007199254740995},"pagination":{"partial":true},"cli":{"location":{"line":2},"kind":"old"}}`)
	diagnostic := &main.CLIFlagRuleDiagnostic{Kind: "invalid_flag_combination", Rule: main.ListFlagRules()[0], Missing: main.ListFlagRules()[0].Requires, HelpArgv: []string{"gregale", "--json", "help", "build", "list"}}
	var out bytes.Buffer
	if err := main.WriteListFlagRuleProblem(&out, original, diagnostic); err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal(original, &before)
	if json.Unmarshal(out.Bytes(), &after) != nil {
		t.Fatal(out.String())
	}
	for key, value := range before {
		if key != "cli" && !bytes.Equal(value, after[key]) {
			t.Fatalf("lost %s: %s", key, out.String())
		}
	}
	var cli map[string]json.RawMessage
	if json.Unmarshal(after["cli"], &cli) != nil || string(cli["kind"]) != `"invalid_flag_combination"` || cli["location"] == nil || cli["rule"] == nil {
		t.Fatalf("CLI fields: %s", out.String())
	}
	decoder := json.NewDecoder(&out)
	if decoder.Decode(new(any)) != nil || decoder.Decode(new(any)) != io.EOF {
		t.Fatal("not exactly one Problem")
	}
	if err := main.WriteListFlagRuleProblem(flagRuleShortWriter{}, original, diagnostic); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	if err := main.WriteListFlagRuleProblem(flagRuleBrokenWriter{}, original, diagnostic); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	for _, invalid := range []string{"null", "[]", "bad"} {
		if err := main.WriteListFlagRuleProblem(io.Discard, []byte(invalid), diagnostic); err == nil {
			t.Fatalf("accepted non-object %q", invalid)
		}
	}
}
