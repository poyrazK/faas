package main_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

type inputLocationView struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}
type inputProblemView struct {
	Code     string
	Category string
	ExitCode int `json:"exit_code"`
	CLI      *struct {
		Kind     string
		Flag     string
		Location *inputLocationView
		HelpArgv []string `json:"help_argv"`
	}
}

func readInputProblem(t *testing.T, args ...string) inputProblemView {
	t.Helper()
	out, stderr, restore := main.SwapIO(t)
	code := main.Run(append([]string{"--json"}, args...))
	text := stderr()
	restore()
	var p inputProblemView
	decoder := json.NewDecoder(strings.NewReader(text))
	if code != 1 || out.Len() != 0 || decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF || p.Code != "invalid_request" || p.Category != "invalid_request" || p.ExitCode != 1 {
		t.Fatalf("input Problem: exit=%d stdout=%s stderr=%s", code, out, text)
	}
	return p
}

// adr: 20261115085307 — structured input locations without payload excerpts.
func TestJSONInputStructuredLocations(t *testing.T) {
	setupJSONHelp(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_json-input-private")
	for _, tc := range []struct {
		name, input string
		line, col   int
	}{
		{"literal", "private-body-value", 1, 1},
		{"unicode", "{\"msg\":\"雪\",\"x\":}", 1, 16},
		{"crlf", "{\r\n\t\"x\":\r\n}", 3, 1},
		{"eof", "{", 1, 2},
		{"trailing", "{} {}", 1, 4},
	} {
		for _, source := range []string{"inline", "file", "stdin"} {
			t.Run(tc.name+"/"+source, func(t *testing.T) {
				argument, path := tc.input, ""
				switch source {
				case "file":
					path = filepath.Join(t.TempDir(), "request.json")
					if err := os.WriteFile(path, []byte(tc.input), 0o600); err != nil {
						t.Fatal(err)
					}
					argument = "@" + path
				case "stdin":
					jsonLocationStdin(t, []byte(tc.input))
					argument = "-"
				}
				p := readInputProblem(t, "invocation", "run", "demo", "--payload", argument)
				if p.CLI == nil || p.CLI.Kind != "invalid_json" || p.CLI.Flag != "--payload" || p.CLI.Location == nil {
					t.Fatalf("missing input metadata: %+v", p)
				}
				want := inputLocationView{Source: source, Path: path, Line: tc.line, Column: tc.col}
				if !reflect.DeepEqual(*p.CLI.Location, want) {
					t.Fatalf("location=%+v want=%+v", *p.CLI.Location, want)
				}
				metadata, err := json.Marshal(p.CLI)
				if err != nil {
					t.Fatal(err)
				}
				for _, private := range []string{"private-body-value", "fp_test_json-input-private", server.URL, "looking for"} {
					if strings.Contains(string(metadata), private) {
						t.Fatal("input metadata exposed payload or credentials")
					}
				}
				if len(p.CLI.HelpArgv) < 3 || p.CLI.HelpArgv[0] != "gregale" {
					t.Fatalf("unsafe help argv: %q", p.CLI.HelpArgv)
				}
				readJSONHelp(t, p.CLI.HelpArgv[1:]...)
			})
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid input or help made %d requests", requests.Load())
	}
}

func TestJSONInputStructuredFlagNames(t *testing.T) {
	setupJSONHelp(t)
	for _, tc := range []struct {
		args []string
		flag string
	}{
		{[]string{"event", "send", "demo", "--type", "sample", "--data", "{"}, "--data"},
		{[]string{"trigger", "create", "--app", "demo", "--kind", "kafka", "--slug", "sample", "--config", "{"}, "--config"},
		{[]string{"event", "schema", "check", "source", "sample", "--version", "v2", "--schema", "{"}, "--schema"},
		{[]string{"event", "schema", "check", "source", "sample", "--version", "v2", "--samples", "{"}, "--samples"},
	} {
		p := readInputProblem(t, tc.args...)
		if p.CLI == nil || p.CLI.Flag != tc.flag || p.CLI.Location == nil || p.CLI.Location.Source != "inline" || p.CLI.Location.Line != 1 || p.CLI.Location.Column != 2 {
			t.Fatalf("flag metadata for %q: %+v", tc.args, p)
		}
	}
}

func TestJSONInputStructuredSourcePaths(t *testing.T) {
	setupJSONHelp(t)
	t.Chdir(t.TempDir())
	const secret = "fp_test_json-path-private"
	t.Setenv("FAAS_TOKEN", secret)
	for _, name := range []string{"stdin", "inline", "request\n\x1b.json", secret + ".json", "invalid-\xff.json"} {
		if err := os.WriteFile(name, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		p := readInputProblem(t, "invocation", "run", "demo", "--payload", "@"+name)
		if p.CLI == nil || p.CLI.Location == nil || p.CLI.Location.Source != "file" {
			t.Fatalf("file misclassified: %q %+v", name, p)
		}
		want := name
		if name == secret+".json" || name == "invalid-\xff.json" {
			want = ""
		}
		if p.CLI.Location.Path != want {
			t.Fatalf("path=%q want=%q", p.CLI.Location.Path, want)
		}
	}
	// A filesystem failure is not a JSON syntax error.
	p := readInputProblem(t, "invocation", "run", "demo", "--payload", "@missing.json")
	if p.CLI != nil {
		t.Fatalf("filesystem failure acquired a syntax location: %+v", p.CLI)
	}
}

func TestJSONInputStructuredWrappedErrors(t *testing.T) {
	setupJSONHelp(t)
	_, err := main.ResolveJSONFlag("--config", "{")
	if err == nil {
		t.Fatal("invalid JSON parsed")
	}
	previous := *main.JsonOutput
	*main.JsonOutput = true
	t.Cleanup(func() { *main.JsonOutput = previous })
	out, stderr, restore := main.SwapIO(t)
	code := main.PrintErr("Invalid config", fmt.Errorf("configuration: %w", err))
	text := stderr()
	restore()
	var p inputProblemView
	if code != 1 || out.Len() != 0 || json.Unmarshal([]byte(text), &p) != nil || p.CLI == nil || p.CLI.Kind != "invalid_json" || p.CLI.Flag != "--config" || p.CLI.Location == nil || p.CLI.Location.Column != 2 {
		t.Fatalf("wrapped input error: exit=%d stdout=%s stderr=%s", code, out, text)
	}
}
