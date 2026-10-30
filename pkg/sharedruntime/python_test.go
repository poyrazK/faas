package sharedruntime

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// pythonPlan loads a Railpack 0.38 Python plan in the shape the builder VM
// produces: deploy.base is the platform runner image.
func pythonPlan(t *testing.T, fixture string, mutate func(map[string]any)) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	p["deploy"].(map[string]any)["base"] = map[string]any{"image": "registry.gregale.dev/runner-python313@sha256:" + "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"}
	if mutate != nil {
		mutate(p)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// spec: §4.6
// ADR-1500 amendment 2: a Railpack Python build whose deploy inputs are the
// toolchain and /app runs its start command as written on the runner base;
// system packages, extra paths and package-manager starts keep the full layer.
func TestClassifyPython(t *testing.T) {
	tests := []struct {
		name       string
		plan       []byte
		wantReason string
		wantStart  []string
	}{
		{name: "requirements.txt", plan: pythonPlan(t, "railpack-0.38.0-py-req.json", nil), wantStart: []string{"/bin/sh", "-c", "python app.py"}},
		{name: "uv project", plan: pythonPlan(t, "railpack-0.38.0-py-uv.json", nil), wantStart: []string{"/bin/sh", "-c", "python app.py"}},
		{name: "gunicorn", plan: pythonPlan(t, "railpack-0.38.0-py-req.json", setStart("gunicorn --bind 0.0.0.0:8080 app:app")), wantStart: []string{"/bin/sh", "-c", "gunicorn --bind 0.0.0.0:8080 app:app"}},
		{name: "uv run", plan: pythonPlan(t, "railpack-0.38.0-py-uv.json", setStart("uv run app.py")), wantReason: ReasonPackageManagerStart},
		{name: "poetry run", plan: pythonPlan(t, "railpack-0.38.0-py-req.json", setStart("poetry run python app.py")), wantReason: ReasonPackageManagerStart},
		{name: "no start", plan: pythonPlan(t, "railpack-0.38.0-py-req.json", setStart(" ")), wantReason: ReasonNoStartCommand},
		{name: "system packages", plan: pythonPlan(t, "railpack-0.38.0-py-req.json", func(p map[string]any) {
			p["deploy"].(map[string]any)["aptPackages"] = []any{"libpq5"}
		}), wantReason: ReasonSystemPackages},
		{name: "extra deploy path", plan: pythonPlan(t, "railpack-0.38.0-py-req.json", func(p map[string]any) {
			d := p["deploy"].(map[string]any)
			d["inputs"] = append(d["inputs"].([]any), map[string]any{"step": "build", "include": []any{"/usr/lib/x86_64-linux-gnu"}})
		}), wantReason: ReasonExtraDeployInput},
		{name: "unreadable", plan: []byte("{"), wantReason: ReasonPlanUnreadable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := ClassifyPython(tc.plan)
			if v.Reason != tc.wantReason || v.Eligible != (tc.wantReason == "") {
				t.Fatalf("verdict = %+v, want reason %q", v, tc.wantReason)
			}
			if !slices.Equal(v.Start, tc.wantStart) {
				t.Fatalf("start = %q, want %q", v.Start, tc.wantStart)
			}
			if v.Eligible && (v.Env["PATH"] != PythonRunnerPath || v.Env["VIRTUAL_ENV"] != "/app/.venv" || v.Env["PYTHONUNBUFFERED"] != "1") {
				t.Fatalf("env = %v", v.Env)
			}
		})
	}
}

// A declared Python version is never replaced: only declarations that all
// name a shared base's minor pick it, and no declaration keeps Railpack's 3.13.
func TestPythonRuntimeForSpecs(t *testing.T) {
	for _, tc := range []struct {
		specs []string
		want  string
	}{
		{nil, RuntimePython313},
		{[]string{"3.12"}, RuntimePython312},
		{[]string{"3.12.4"}, RuntimePython312},
		{[]string{"3.12.x"}, RuntimePython312},
		{[]string{"python-3.12.4"}, RuntimePython312},
		{[]string{"3.13", "python-3.13.1"}, RuntimePython313},
		{[]string{"3.12", "3.13"}, ""},
		{[]string{"3.11"}, ""},
		{[]string{"3.14"}, ""},
		{[]string{"3"}, ""},
		{[]string{"pypy3.10"}, ""},
		{[]string{"system"}, ""},
		{[]string{">=3.12"}, ""},
		{[]string{`["3.12", "3.11"]`}, ""},
	} {
		if got := PythonRuntimeForSpecs(tc.specs); got != tc.want {
			t.Errorf("PythonRuntimeForSpecs(%q) = %q, want %q", tc.specs, got, tc.want)
		}
	}
}

// The virtualenv Railpack builds with mise's Python moves to the runner
// base's interpreter; nothing else may point into the dropped toolchain.
func TestRelinkVenvLink(t *testing.T) {
	const interp = "/usr/bin/python3.13"
	for _, tc := range []struct {
		name, target, want string
		ok                 bool
	}{
		{"app/.venv/bin/python", "/mise/installs/python/3.13.15/bin/python3.13", interp, true},
		{"app/.venv/bin/python3", "python", "python", true},
		{"app/.venv/bin/python3.13", "/mise/installs/python/3.13.15/bin/python3.13", interp, true},
		{"app/.venv/lib64", "lib", "lib", true},
		{"app/.venv/bin/uv", "/mise/installs/uv/0.9.0/uv", "", false},
		{"app/node_modules/x", "/mise/installs/python/3.13.15/bin/python3.13", "", false},
		{"app/.venv/bin/python3.x", "/mise/installs/python/3.13.15/bin/python3.13", "", false},
		{"app/static/logo", "/app/assets/logo.png", "/app/assets/logo.png", true},
	} {
		got, ok := RelinkVenvLink(tc.name, tc.target, interp)
		if got != tc.want || ok != tc.ok {
			t.Errorf("RelinkVenvLink(%q, %q) = %q, %v; want %q, %v", tc.name, tc.target, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRewritePyvenvConfig(t *testing.T) {
	const interp = "/usr/local/bin/python3.12"
	venv := "home = /mise/installs/python/3.12.12/bin\ninclude-system-site-packages = false\nversion = 3.12.12\n" +
		"executable = /mise/installs/python/3.12.12/bin/python3.12\ncommand = /mise/installs/python/3.12.12/bin/python3.12 -m venv /app/.venv\n"
	got, ok := RewritePyvenvConfig([]byte(venv), interp)
	want := "home = /usr/local/bin\ninclude-system-site-packages = false\nversion = 3.12.12\n" +
		"executable = /usr/local/bin/python3.12\ncommand = /mise/installs/python/3.12.12/bin/python3.12 -m venv /app/.venv\n"
	if !ok || string(got) != want {
		t.Fatalf("rewrite = %q, %v\nwant %q", got, ok, want)
	}
	uv := "home = /mise/installs/python/3.12.12/bin\nimplementation = CPython\nuv = 0.9.2\nversion_info = 3.12.12\ninclude-system-site-packages = false\n"
	if got, ok := RewritePyvenvConfig([]byte(uv), interp); !ok || string(got) != "home = /usr/local/bin\nimplementation = CPython\nuv = 0.9.2\nversion_info = 3.12.12\ninclude-system-site-packages = false\n" {
		t.Fatalf("uv rewrite = %q, %v", got, ok)
	}
	for _, body := range []string{"include-system-site-packages = false\n", "home = /usr/bin\n"} {
		if _, ok := RewritePyvenvConfig([]byte(body), interp); ok {
			t.Errorf("RewritePyvenvConfig(%q) accepted a venv not built on the toolchain", body)
		}
	}
}
