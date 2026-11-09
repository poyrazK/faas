package gregalemanifest_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// adr: 20261107193000
func TestReadInlineConfigShapes(t *testing.T) {
	cases := []struct {
		name, file, src string
		wantCrons       []string
		wantRAM         int
		wantQueue       string
		wantEnv         map[string]string
	}{
		{
			name: "node object", file: "handler.js",
			src: `// digest
import pg from "pg";
export const config = {
  cron: "0 8 * * *",   // daily
  ramMb: 512,
  /* block */ env: { LOG_LEVEL: 'info', apiBase: "x" },
};
export default { async fetch() { return new Response("ok"); } };
`,
			wantCrons: []string{"0 8 * * *"}, wantRAM: 512, wantEnv: map[string]string{"LOG_LEVEL": "info", "apiBase": "x"},
		},
		{
			name: "node defineConfig", file: "handler.mjs",
			src: `import { defineConfig } from "@gregale/sdk-node";
export const config = defineConfig({ queue: "invoice-jobs", request_timeout_s: 60, cron: ["*/5 * * * *", "0 0 * * *"] });
export async function handler(event, ctx) { return { body: "ok" }; }
`,
			wantCrons: []string{"*/5 * * * *", "0 0 * * *"}, wantQueue: "invoice-jobs",
		},
		{
			name: "python dict", file: "handler.py",
			src: `import json

config = {
    "cron": "0 8 * * *",  # daily
    "ram_mb": 1_024,
    "env": {"LOG_LEVEL": "debug"},
}

def handler(event, ctx):
    return {"body": "ok"}
`,
			wantCrons: []string{"0 8 * * *"}, wantRAM: 1024, wantEnv: map[string]string{"LOG_LEVEL": "debug"},
		},
		{
			name: "python decorator", file: "handler.py",
			src: `from faas_sdk import function

@function(queue="thumbs", ram_mb=256,
          cron="0 * * * *")
async def handler(event, ctx):
    return {"body": "ok"}
`,
			wantCrons: []string{"0 * * * *"}, wantRAM: 256, wantQueue: "thumbs",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{tc.file: tc.src})
			cfg, found, err := gregalemanifest.ReadInlineConfig(dir, tc.file)
			if err != nil || !found {
				t.Fatalf("ReadInlineConfig = found %v, err %v", found, err)
			}
			if !reflect.DeepEqual(cfg.Crons, tc.wantCrons) {
				t.Errorf("crons = %q, want %q", cfg.Crons, tc.wantCrons)
			}
			if cfg.Spec.RAMMB != tc.wantRAM {
				t.Errorf("ram_mb = %d, want %d", cfg.Spec.RAMMB, tc.wantRAM)
			}
			if tc.wantQueue != "" {
				if len(cfg.Spec.QueueBindings) != 1 || cfg.Spec.QueueBindings[0].Name != tc.wantQueue || cfg.Spec.QueueBindings[0].QueueName != tc.wantQueue {
					t.Errorf("queue_bindings = %+v, want one named %q", cfg.Spec.QueueBindings, tc.wantQueue)
				}
			}
			if tc.wantEnv != nil && !reflect.DeepEqual(cfg.Spec.Env, tc.wantEnv) {
				t.Errorf("env = %v, want %v (env keys keep their spelling)", cfg.Spec.Env, tc.wantEnv)
			}
		})
	}
}

func TestReadInlineConfigNoneDeclared(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"handler.js": "export default { fetch() {} };\nconst config = { cron: 'x' };\n",
		"handler.py": "from modal import App\napp = App()\n\n@app.function(gpu=GPU)\ndef train():\n    pass\n\ndef handler(event, ctx):\n    return {}\n",
		"main.go":    "package main\n",
	})
	for _, f := range []string{"handler.js", "handler.py", "main.go", "missing.js"} {
		if _, found, err := gregalemanifest.ReadInlineConfig(dir, f); found || err != nil {
			t.Errorf("%s: found %v err %v, want neither", f, found, err)
		}
	}
}

func TestReadInlineConfigRejects(t *testing.T) {
	cases := map[string]struct{ file, src, want string }{
		"js variable":       {"handler.js", "export const config = { cron: SCHEDULE };", `line 1: config must contain only literal values; "SCHEDULE" is an expression`},
		"js env":            {"handler.js", "export const config = {\n  ramMb: process.env.RAM,\n};", `line 2: config must contain only literal values; "process.env.RAM"`},
		"js spread":         {"handler.js", "export const config = { ...base };", "spreads are not allowed"},
		"js template":       {"handler.js", "export const config = { cron: `0 ${h} * * *` };", "template substitutions are not allowed"},
		"js not object":     {"handler.js", "export const config = makeConfig();", "must be an object literal"},
		"js twice":          {"handler.js", "export const config = {};\nexport const config = {};", "line 2: config is exported more than once"},
		"unknown key":       {"handler.js", "export const config = { ramMB: 1, crn: 'x' };", `unknown key "crn"; did you mean "cron"?`},
		"forbidden key":     {"handler.js", "export const config = { name: 'x' };", `"name" cannot be set in code`},
		"wrong type":        {"handler.js", "export const config = { ram_mb: 'big' };", `"big" is not a valid int`},
		"cron type":         {"handler.js", "export const config = { cron: 5 };", "cron must be a string or a list of strings"},
		"queue and binding": {"handler.js", "export const config = { queue: 'a', queueBindings: [] };", "set either queue or queue_bindings"},
		"py unquoted key":   {"handler.py", "config = {cron: 'x'}\n", "dict keys must be quoted strings"},
		"py both":           {"handler.py", "config = {}\n\n@function(ram_mb=1)\ndef handler(event, ctx): pass\n", "declare config once"},
		"py positional":     {"handler.py", "@function('0 8 * * *')\ndef handler(event, ctx): pass\n", "keyword arguments"},
		"py triple":         {"handler.py", "config = {\"cron\": \"\"\"x\"\"\"}\n", "triple-quoted"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{tc.file: tc.src})
			_, _, err := gregalemanifest.ReadInlineConfig(dir, tc.file)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), tc.file+": ") {
				t.Errorf("err = %v, want it to name %s", err, tc.file)
			}
		})
	}
}

// adr: 20261107193000
func TestApplyInlineConfigYAMLWins(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"gregale.yaml": `functions:
  - name: digest
    root: functions/digest
    ram_mb: 256
  - name: plain
    root: functions/plain
  - name: scheduled
    root: functions/scheduled
triggers:
  - kind: cron
    app: scheduled
    schedule: "0 1 * * *"
    path: /run
`,
		"functions/digest/handler.js":    "export const config = { ramMb: 1024, requestTimeoutS: 60, cron: '0 8 * * *' };",
		"functions/plain/handler.py":     "def handler(event, ctx):\n    return {}\n",
		"functions/scheduled/handler.js": "export const config = { cron: '0 9 * * *', concurrency: 4 };",
	})
	m, found, err := gregalemanifest.Load(dir)
	if err != nil || !found {
		t.Fatalf("Load: %v %v", found, err)
	}
	m, sources, err := gregalemanifest.ApplyInlineConfig(m, dir)
	if err != nil {
		t.Fatal(err)
	}
	digest := m.Functions[0]
	if digest.RAMMB != 256 || digest.RequestTimeoutS != 60 {
		t.Errorf("digest ram/timeout = %d/%d, want yaml's 256 and code's 60", digest.RAMMB, digest.RequestTimeoutS)
	}
	if m.Functions[2].Concurrency != 4 {
		t.Errorf("scheduled concurrency = %d, want 4 from code", m.Functions[2].Concurrency)
	}
	var crons []string
	for _, tr := range m.Triggers {
		crons = append(crons, tr.App+" "+tr.Schedule+" "+tr.Path)
	}
	want := []string{"scheduled 0 1 * * * /run", "digest 0 8 * * * /"}
	if !reflect.DeepEqual(crons, want) {
		t.Errorf("triggers = %q, want %q (a yaml cron replaces code crons for that function)", crons, want)
	}
	wantSources := []gregalemanifest.InlineConfigSource{
		{Function: "digest", File: "functions/digest/handler.js", Keys: []string{"cron", "request_timeout_s"}},
		{Function: "scheduled", File: "functions/scheduled/handler.js", Keys: []string{"concurrency"}},
	}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Errorf("sources = %+v, want %+v", sources, wantSources)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("merged manifest invalid: %v", err)
	}
}

// adr: 20261107193000
func TestApplyInlineConfigDiscovers(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"functions/digest/handler.js": "export const config = { cron: '0 8 * * *' };",
		"functions/thumbs/handler.py": "config = {\"queue\": \"thumbs\"}\n",
		"functions/legacy/handler.js": "export default { fetch() {} };",
		"functions/.cache/handler.js": "export const config = {};",
		"README.md":                   "x",
	})
	m, sources, err := gregalemanifest.ApplyInlineConfig(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil || len(m.Functions) != 2 {
		t.Fatalf("functions = %+v, want digest and thumbs", m)
	}
	if m.Functions[0].Name != "digest" || m.Functions[0].Root != "functions/digest" || m.Functions[1].Name != "thumbs" {
		t.Errorf("functions = %+v", m.Functions)
	}
	if len(m.Triggers) != 1 || m.Triggers[0].App != "digest" {
		t.Errorf("triggers = %+v", m.Triggers)
	}
	if len(sources) != 2 || !sources[0].Discovered {
		t.Errorf("sources = %+v", sources)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("discovered manifest invalid: %v", err)
	}
}

func TestApplyInlineConfigNoDiscovery(t *testing.T) {
	files := map[string]string{"functions/digest/handler.js": "export const config = { cron: '0 8 * * *' };"}
	// A single-app manifest keeps deploying as one app.
	single := writeFiles(t, map[string]string{"gregale.yaml": "function:\n  runtime: node24\n", "functions/digest/handler.js": files["functions/digest/handler.js"]})
	m, _, _ := gregalemanifest.Load(single)
	got, sources, err := gregalemanifest.ApplyInlineConfig(m, single)
	if err != nil || len(got.Functions) != 0 || sources != nil {
		t.Errorf("single-app manifest: functions %+v sources %+v err %v, want untouched", got.Functions, sources, err)
	}
	// Directories without a config are never discovered.
	none := writeFiles(t, map[string]string{"functions/a/handler.js": "export default {};"})
	if got, _, err := gregalemanifest.ApplyInlineConfig(nil, none); got != nil || err != nil {
		t.Errorf("no configs: manifest %+v err %v, want nil", got, err)
	}
	// An invalid directory name with a config is an error, not a silent skip.
	bad := writeFiles(t, map[string]string{"functions/Digest_Job/handler.js": "export const config = {};"})
	if _, _, err := gregalemanifest.ApplyInlineConfig(nil, bad); err == nil || !strings.Contains(err.Error(), "not a valid function name") {
		t.Errorf("err = %v", err)
	}
}
