package sharedruntime

import (
	"slices"
	"testing"
)

// spec: §4.6
// ADR-1500 amendment 3: a package manager's `run` in front of a Python start
// command is dropped when the app layer shows that running the command with
// the virtualenv first on PATH is all it would do; anything else keeps the
// full layer.
func TestResolvePythonStart(t *testing.T) {
	layer := &PythonLayer{
		Scripts:       map[string]bool{"python": true, "gunicorn": true, "uvicorn": true, "serve": true},
		InlineScripts: map[string]bool{"tool.py": true},
	}
	dotEnv := &PythonLayer{Scripts: layer.Scripts, DotEnv: true}
	tests := []struct {
		name       string
		command    string
		layer      *PythonLayer
		want       string
		wantReason string
	}{
		{name: "plain", command: "gunicorn app:app", layer: layer, want: "gunicorn app:app"},
		{name: "uv run script", command: "uv run gunicorn --bind 0.0.0.0:$PORT app:app", layer: layer, want: "gunicorn --bind 0.0.0.0:$PORT app:app"},
		{name: "uv run file", command: "uv run main.py", layer: layer, want: "python main.py"},
		{name: "uv run module", command: "uv run -m uvicorn main:app", layer: layer, want: "python -m uvicorn main:app"},
		{name: "uv run sync flags", command: "uv run --frozen --no-dev uvicorn main:app", layer: layer, want: "uvicorn main:app"},
		{name: "uv flag before run", command: "uv --quiet run uvicorn main:app", layer: layer, want: "uvicorn main:app"},
		{name: "uv run separator", command: "uv run -- uvicorn main:app", layer: layer, want: "uvicorn main:app"},
		{name: "uv run python", command: "uv run python -m flask run", layer: layer, want: "python -m flask run"},
		{name: "toolchain path", command: "/mise/shims/uv run uvicorn main:app", layer: layer, want: "uvicorn main:app"},
		{name: "poetry run", command: "poetry run gunicorn app:app", layer: layer, want: "gunicorn app:app"},
		{name: "poetry run path", command: "poetry run ./bin/start", layer: layer, want: "./bin/start"},
		{name: "pdm run", command: "pdm run uvicorn main:app", layer: layer, want: "uvicorn main:app"},
		{name: "pipenv run", command: "pipenv run gunicorn app:app", layer: layer, want: "gunicorn app:app"},
		{name: "exec and assignment", command: "WEB_CONCURRENCY=2 exec uv run gunicorn app:app", layer: layer, want: "WEB_CONCURRENCY=2 exec gunicorn app:app"},
		{name: "compound", command: "python manage.py migrate && poetry run gunicorn app:app", layer: layer, want: "python manage.py migrate && gunicorn app:app"},
		{name: "two runs", command: "uv run python manage.py migrate; uv run gunicorn app:app", layer: layer, want: "python manage.py migrate; gunicorn app:app"},
		{name: "quoted arguments", command: `uv run gunicorn --access-logformat '%(h)s "%(r)s"' app:app 2>&1`, layer: layer, want: `gunicorn --access-logformat '%(h)s "%(r)s"' app:app 2>&1`},
		{name: "unknown layer", command: "poetry run serve-later", want: "serve-later"},
		{name: "poetry script", command: "poetry run start", layer: layer, wantReason: ReasonRunTargetUnresolved},
		{name: "system program", command: "pdm run sh -c 'gunicorn app:app'", layer: layer, wantReason: ReasonRunTargetUnresolved},
		{name: "pipenv dotenv", command: "pipenv run gunicorn app:app", layer: dotEnv, wantReason: ReasonPipenvDotEnv},
		{name: "inline metadata", command: "uv run tool.py", layer: layer, wantReason: ReasonInlineScriptMetadata},
		{name: "uv run with", command: "uv run --with httpx uvicorn main:app", layer: layer, wantReason: ReasonPackageManagerStart},
		{name: "uv project dir", command: "uv --directory src run uvicorn main:app", layer: layer, wantReason: ReasonPackageManagerStart},
		{name: "uvx", command: "uvx gunicorn app:app", layer: layer, wantReason: ReasonPackageManagerStart},
		{name: "hatch", command: "hatch run serve", layer: layer, wantReason: ReasonPackageManagerStart},
		{name: "poetry other command", command: "poetry install && poetry run serve", layer: layer, wantReason: ReasonPackageManagerStart},
		{name: "bare run", command: "uv run", layer: layer, wantReason: ReasonPackageManagerStart},
		{name: "later in a pipeline", command: "cd src && uv run --with x app", layer: layer, wantReason: ReasonPackageManagerStart},
		{name: "subshell with tool", command: "(cd src && uv run app.py)", layer: layer, wantReason: ReasonPackageManagerStart},
		{name: "substitution without tool", command: "gunicorn --workers $(nproc) app:app", layer: layer, want: "gunicorn --workers $(nproc) app:app"},
		{name: "empty", command: "  ", layer: layer, wantReason: ReasonNoStartCommand},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := ResolvePythonStart(tc.command, tc.layer)
			var want []string
			if tc.wantReason == "" {
				want = []string{"/bin/sh", "-c", tc.want}
			}
			if reason != tc.wantReason || !slices.Equal(got, want) {
				t.Fatalf("ResolvePythonStart(%q) = %q (%q), want %q (%q)", tc.command, got, reason, want, tc.wantReason)
			}
		})
	}
}

func TestSplitShellCommands(t *testing.T) {
	commands, ok := splitShellCommands(`A=1 uv run "my app" 'x y'\ z >out 2>&1 && echo "a\"b" | tee log & # done`)
	if !ok {
		t.Fatal("refused plain shell syntax")
	}
	var got [][]string
	for _, words := range commands {
		var texts []string
		for _, w := range words {
			texts = append(texts, w.text)
		}
		got = append(got, texts)
	}
	want := [][]string{{"A=1", "uv", "run", "my app", "x y z", ">out", "2>&1"}, {"echo", `a"b`}, {"tee", "log"}}
	if len(got) != len(want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Fatalf("command %d = %q, want %q", i, got[i], want[i])
		}
	}
	for _, refused := range []string{"(a)", "{ a; }", "a `b`", `a "$(b)"`, "cat <<EOF", "echo 'open", `echo "open`} {
		if _, ok := splitShellCommands(refused); ok {
			t.Errorf("splitShellCommands(%q) accepted syntax it does not follow", refused)
		}
	}
}
