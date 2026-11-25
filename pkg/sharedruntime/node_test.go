package sharedruntime

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// platformPlan rewrites Railpack's upstream Node plan into the shape the
// builder VM produces: deploy.base is the platform image and no apt step runs.
func platformPlan(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/railpack-0.38.0-node-plain.json")
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	deploy := p["deploy"].(map[string]any)
	deploy["base"] = map[string]any{"image": "registry.gregale.dev/runner-node24@sha256:" + "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"}
	if mutate != nil {
		mutate(p)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func setStart(command string) func(map[string]any) {
	return func(p map[string]any) { p["deploy"].(map[string]any)["startCommand"] = command }
}

// spec: §4.6
// H10-140: a Node build that needs only /app on the runner base is eligible
// and starts without npm; anything Railpack installs outside /app, or any npm
// behavior the platform would only approximate, keeps the full layer.
func TestClassifyNode(t *testing.T) {
	upstream, err := os.ReadFile("testdata/railpack-0.38.0-node-plain.json")
	if err != nil {
		t.Fatal(err)
	}
	const pkg = `{"scripts":{"start":"node worker.js","dev":"node --watch worker.js"}}`
	tests := []struct {
		name       string
		plan       []byte
		pkg        string
		wantReason string
		wantStart  []string
	}{
		{name: "npm start script", plan: platformPlan(t, nil), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node worker.js"}},
		{name: "npm start shorthand", plan: platformPlan(t, setStart("npm start")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node worker.js"}},
		{name: "named script", plan: platformPlan(t, setStart("npm run dev")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node --watch worker.js"}},
		{name: "direct command", plan: platformPlan(t, setStart("node server.js")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node server.js"}},
		{name: "upstream apt base", plan: upstream, pkg: pkg, wantReason: ReasonSystemPackages},
		{name: "apt packages declared", plan: platformPlan(t, func(p map[string]any) {
			p["deploy"].(map[string]any)["aptPackages"] = []string{"ffmpeg"}
		}), pkg: pkg, wantReason: ReasonSystemPackages},
		{name: "deploy path outside app", plan: platformPlan(t, func(p map[string]any) {
			d := p["deploy"].(map[string]any)
			d["inputs"] = append(d["inputs"].([]any), map[string]any{"step": "build", "include": []string{"/usr/share/fonts"}})
		}), pkg: pkg, wantReason: ReasonExtraDeployInput},
		{name: "yarn through corepack", plan: platformPlan(t, func(p map[string]any) {
			// Railpack 0.38 with a packageManager field (usesCorepack).
			d := p["deploy"].(map[string]any)
			d["startCommand"] = "yarn start"
			d["inputs"] = append(d["inputs"].([]any), map[string]any{"step": "build", "include": []string{"/root/.cache", ".", "/opt/corepack", "/mise/installs", "/mise/shims"}})
		}), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node worker.js"}},
		{name: "toolchain step reaching outside mise", plan: platformPlan(t, func(p map[string]any) {
			d := p["deploy"].(map[string]any)
			d["inputs"] = append(d["inputs"].([]any), map[string]any{"step": "packages:mise", "include": []string{"/opt/chrome"}})
		}), pkg: pkg, wantReason: ReasonExtraDeployInput},
		{name: "lifecycle hook", plan: platformPlan(t, nil), pkg: `{"scripts":{"prestart":"node migrate.js","start":"node worker.js"}}`, wantReason: ReasonNPMLifecycleHooks},
		{name: "missing script", plan: platformPlan(t, nil), pkg: `{"scripts":{}}`, wantReason: ReasonMissingScript},
		{name: "forwarded npm arguments", plan: platformPlan(t, setStart("npm run start -- --port 3000")), pkg: pkg, wantReason: ReasonNPMArguments},
		// ADR-20261125170000: pnpm and yarn starts resolve like npm's.
		{name: "yarn start", plan: platformPlan(t, setStart("yarn start")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node worker.js"}},
		{name: "yarn run script", plan: platformPlan(t, setStart("yarn run dev")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node --watch worker.js"}},
		{name: "yarn script shorthand", plan: platformPlan(t, setStart("yarn dev")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node --watch worker.js"}},
		{name: "pnpm start", plan: platformPlan(t, setStart("pnpm start")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node worker.js"}},
		{name: "pnpm run script", plan: platformPlan(t, setStart("pnpm run dev")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node --watch worker.js"}},
		{name: "pnpm script shorthand", plan: platformPlan(t, setStart("pnpm dev")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "node --watch worker.js"}},
		{name: "pnpm shorthand without that script", plan: platformPlan(t, setStart("pnpm serve")), pkg: pkg, wantReason: ReasonPackageManagerStart},
		{name: "pnpm command, not a script", plan: platformPlan(t, setStart("pnpm exec node worker.js")), pkg: pkg, wantReason: ReasonPackageManagerStart},
		{name: "yarn shorthand with arguments", plan: platformPlan(t, setStart("yarn dev --inspect")), pkg: pkg, wantReason: ReasonNPMArguments},
		{name: "pnpm forwarded arguments", plan: platformPlan(t, setStart("pnpm run start -- --port 3000")), pkg: pkg, wantReason: ReasonNPMArguments},
		{name: "pnpm lifecycle hook", plan: platformPlan(t, setStart("pnpm start")), pkg: `{"scripts":{"prestart":"node migrate.js","start":"node worker.js"}}`, wantReason: ReasonNPMLifecycleHooks},
		{name: "npx start", plan: platformPlan(t, setStart("npx serve")), pkg: pkg, wantReason: ReasonPackageManagerStart},
		// The runner base ships no package manager: a start script that calls
		// one, directly or through a script it reaches, would exit 127.
		{name: "start script calling pnpm", plan: platformPlan(t, setStart("pnpm start")), pkg: `{"scripts":{"start":"pnpm run serve","serve":"node worker.js"}}`, wantReason: ReasonScriptPackageManager},
		{name: "start script calling npm", plan: platformPlan(t, setStart("npm start")), pkg: `{"scripts":{"start":"npm run build && node worker.js","build":"tsc"}}`, wantReason: ReasonScriptPackageManager},
		{name: "start script reaching a script runner", plan: platformPlan(t, setStart("yarn start")), pkg: `{"scripts":{"start":"run-p api web","api":"node api.js","web":"node web.js"}}`, wantReason: ReasonScriptPackageManager},
		{name: "start script running node only", plan: platformPlan(t, setStart("yarn start")), pkg: `{"scripts":{"start":"NODE_ENV=production node --enable-source-maps dist/main.js"}}`, wantStart: []string{"/bin/sh", "-c", "NODE_ENV=production node --enable-source-maps dist/main.js"}},
		{name: "bun keeps its own install", plan: platformPlan(t, setStart("bun run start")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "bun run start"}},
		{name: "env-prefixed bun", plan: platformPlan(t, setStart("PORT=3000 bun run start")), pkg: pkg, wantStart: []string{"/bin/sh", "-c", "PORT=3000 bun run start"}},
		{name: "bun script calling npm", plan: platformPlan(t, setStart("bun run --bun start")), pkg: `{"scripts":{"start":"npm run serve"}}`, wantReason: ReasonPackageManagerStart},
		{name: "npm script running bun", plan: platformPlan(t, setStart("npm start")), pkg: `{"scripts":{"start":"bun index.ts"}}`, wantReason: ReasonBunInScript},
		{name: "no start command", plan: platformPlan(t, setStart("")), pkg: pkg, wantReason: ReasonNoStartCommand},
		{name: "unreadable plan", plan: []byte("{"), pkg: pkg, wantReason: ReasonPlanUnreadable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyNode(tt.plan, []byte(tt.pkg))
			if got.Reason != tt.wantReason || got.Eligible != (tt.wantReason == "") {
				t.Fatalf("verdict = %+v, want reason %q", got, tt.wantReason)
			}
			if !slices.Equal(got.Start, tt.wantStart) {
				t.Fatalf("start = %q, want %q", got.Start, tt.wantStart)
			}
			wantPath := runnerPath
			if len(tt.wantStart) > 0 && UsesBun(tt.wantStart[len(tt.wantStart)-1]) {
				wantPath = "" // imaged sets it from the artifact's Bun install
			}
			if got.Eligible && (got.Env["PATH"] != wantPath || got.Env["NODE_ENV"] != "production") {
				t.Fatalf("env = %v, want the runner PATH and the plan's variables", got.Env)
			}
		})
	}
}

func TestDroppableOutsideApp(t *testing.T) {
	for name, want := range map[string]bool{
		"mise/installs/node/24.1.0/bin/node":  true,
		"usr/local/bin/mise":                  true,
		"etc/mise/config.toml":                true,
		"root/.local/state/mise/x":            true,
		"root/.cache/npm/_cacache/x":          true,
		"opt/corepack/v1/yarn/1.22.22/x.js":   true,
		"opt/corepacked/x":                    false,
		"usr/lib/x86_64-linux-gnu/libvips.so": false,
		"etc/passwd":                          false,
		"misery/file":                         false,
	} {
		if got := DroppableOutsideApp(name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}
