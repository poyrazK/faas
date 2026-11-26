package sharedruntime

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const nextPkg = `{"scripts":{"dev":"next dev","build":"next build","start":"next start"},"dependencies":{"next":"^16.0.0","react":"19.3.0"}}`

// nextPlan is the platform Node plan with Railpack's Next.js build step.
func nextPlan(t *testing.T, buildCmd string) []byte {
	t.Helper()
	return platformPlan(t, func(p map[string]any) {
		for _, raw := range p["steps"].([]any) {
			step := raw.(map[string]any)
			if step["name"] == "build" {
				step["commands"] = []any{map[string]any{"cmd": buildCmd}}
				step["variables"] = map[string]any{"NEXT_TELEMETRY_DISABLED": "1"}
			}
		}
	})
}

func buildStep(t *testing.T, plan []byte) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(plan, &p); err != nil {
		t.Fatal(err)
	}
	for _, raw := range p["steps"].([]any) {
		if step := raw.(map[string]any); step["name"] == "build" {
			return step
		}
	}
	t.Fatal("no build step")
	return nil
}

// testCapMB is the Free plan's app layer cap.
const testCapMB = 256

// spec: §4.6
// ADR-20261124160000: a Next.js 16 app started with a plain `next start` is
// built with the standalone adapter on its build command only, then runs the
// step that keeps the standalone tree only for an app over its plan's cap.
// Anything else keeps Railpack's plan.
func TestStampNextStandalone(t *testing.T) {
	stamped, err := StampNextStandalone(nextPlan(t, "npm run build"), []byte(nextPkg), testCapMB, 0)
	if err != nil {
		t.Fatal(err)
	}
	build := buildStep(t, stamped)
	commands := build["commands"].([]any)
	if len(commands) != 3 {
		t.Fatalf("commands = %v, want adapter, build, copy", commands)
	}
	write := commands[0].(map[string]any)
	if write["path"] != nextAdapterPath || write["name"] != nextAdapterAsset {
		t.Fatalf("first command = %v, want the adapter file", write)
	}
	if got := commands[1].(map[string]any)["cmd"]; got != "env NEXT_ADAPTER_PATH="+nextAdapterPath+" npm run build" {
		t.Fatalf("build command = %q", got)
	}
	if got := commands[2].(map[string]any)["cmd"]; got != nextStandaloneCommand(testCapMB, 0) ||
		!strings.Contains(got.(string), " -gt 268435456 ") {
		t.Fatalf("last command = %q, want the standalone step at a 256 MB cap", got)
	}
	if _, err := StampNextStandalone(nextPlan(t, "npm run build"), []byte(nextPkg), 0, 0); !errors.Is(err, ErrNotNextStandalone) {
		t.Fatalf("no cap = %v, want the plan left alone", err)
	}
	if build["assets"].(map[string]any)[nextAdapterAsset] != nextAdapter {
		t.Fatal("adapter asset missing")
	}
	// A step variable would reach the image environment.
	if _, ok := build["variables"].(map[string]any)["NEXT_ADAPTER_PATH"]; ok {
		t.Fatal("NEXT_ADAPTER_PATH is a step variable")
	}
	if strings.Contains(string(stamped), `"startCommand":"node`) {
		t.Fatal("deploy start command changed; imaged chooses the standalone tree")
	}
	if _, err := StampNextStandalone(stamped, []byte(nextPkg), testCapMB, 0); !errors.Is(err, ErrNotNextStandalone) {
		t.Fatalf("second stamp = %v, want it left alone", err)
	}

	tests := []struct {
		name string
		plan []byte
		pkg  string
	}{
		{name: "next 15", plan: nextPlan(t, "npm run build"), pkg: strings.Replace(nextPkg, "^16.0.0", "15.5.0", 1)},
		{name: "next only a dev dependency", plan: nextPlan(t, "npm run build"), pkg: `{"scripts":{"build":"next build","start":"next start"},"devDependencies":{"next":"16.4.6"}}`},
		{name: "start with a project directory", plan: nextPlan(t, "npm run build"), pkg: strings.Replace(nextPkg, `"next start"`, `"next start ./web"`, 1)},
		{name: "start with an unknown option", plan: nextPlan(t, "npm run build"), pkg: strings.Replace(nextPkg, `"next start"`, `"next start --experimental-https"`, 1)},
		{name: "custom server", plan: nextPlan(t, "npm run build"), pkg: strings.Replace(nextPkg, `"next start"`, `"node server.js"`, 1)},
		{name: "prestart hook", plan: nextPlan(t, "npm run build"), pkg: strings.Replace(nextPkg, `"start"`, `"prestart":"node migrate.js","start"`, 1)},
		{name: "customized build command", plan: nextPlan(t, "npm run build:prod"), pkg: nextPkg},
		{name: "deploy start is not the start script", plan: platformPlan(t, setStart("node server.js")), pkg: nextPkg},
		{name: "unreadable package.json", plan: nextPlan(t, "npm run build"), pkg: "{"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := StampNextStandalone(tt.plan, []byte(tt.pkg), testCapMB, 0); !errors.Is(err, ErrNotNextStandalone) {
				t.Fatalf("err = %v, want ErrNotNextStandalone", err)
			}
		})
	}
	// ADR-20261124160000 amendment 2: listen flags the standalone server
	// takes from its environment do not stop the stamp.
	flagged := strings.Replace(nextPkg, `"next start"`, `"next start -p $PORT -H 0.0.0.0"`, 1)
	if _, err := StampNextStandalone(nextPlan(t, "npm run build"), []byte(flagged), testCapMB, 0); err != nil {
		t.Fatalf("start with listen flags: %v", err)
	}
	for _, pm := range []string{"pnpm", "yarn", "bun"} {
		plan := platformPlan(t, setStart(pm+" run start"))
		var p map[string]any
		_ = json.Unmarshal(plan, &p)
		for _, raw := range p["steps"].([]any) {
			if step := raw.(map[string]any); step["name"] == "build" {
				step["commands"] = []any{map[string]any{"cmd": pm + " run build"}}
			}
		}
		plan, _ = json.Marshal(p)
		if _, err := StampNextStandalone(plan, []byte(nextPkg), testCapMB, 0); err != nil {
			t.Fatalf("%s: %v", pm, err)
		}
	}
}

func TestNextStart(t *testing.T) {
	tests := []struct {
		command string
		pkg     string
		want    bool
	}{
		{"next start", "", true},
		{"npm run start", nextPkg, true},
		{"npm start", nextPkg, true},
		{"pnpm run start", nextPkg, true},
		{"bun run start", nextPkg, true},
		{"npm run start -- -p 3000", nextPkg, false},
		{"next start -p 3000", "", true},
		{"npm start", `{"scripts":{"start":"next start -p $PORT -H 0.0.0.0"}}`, true},
		{"npm start", `{"scripts":{"start":"next start ./site"}}`, false},
		{"npm start", `{"scripts":{"start":"next start --turbo"}}`, false},
		{"npm run serve", nextPkg, false},
		{"npx next start", nextPkg, false},
		{"npm run start", `{"scripts":{"start":"next start","poststart":"echo"}}`, false},
		{"npm run start", `{"scripts":{"start":"node server.js"}}`, false},
	}
	for _, tt := range tests {
		if got := NextStart(tt.command, []byte(tt.pkg)); got != tt.want {
			t.Errorf("NextStart(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

// ADR-20261124160000 amendment 2: `next start` flags the standalone server
// reads from its environment become that environment, and a flag still
// overrides the variable; anything else keeps the app's own start.
func TestNextStandaloneStartFor(t *testing.T) {
	plain := NextStandaloneStart
	with := func(env string) []string {
		return []string{"/bin/sh", "-c", "exec env " + env + " node /app/.next/standalone/server.js"}
	}
	script := func(start string) string {
		b, _ := json.Marshal(map[string]any{"scripts": map[string]string{"start": start}})
		return string(b)
	}
	tests := []struct {
		name    string
		command string
		pkg     string
		want    []string
	}{
		{name: "plain", command: "npm start", pkg: script("next start"), want: plain},
		{name: "port", command: "npm start", pkg: script("next start -p 3000"), want: with("PORT=3000")},
		{name: "long port", command: "pnpm start", pkg: script("next start --port 3000"), want: with("PORT=3000")},
		{name: "port with equals", command: "yarn start", pkg: script("next start --port=3000"), want: with("PORT=3000")},
		{name: "port variable", command: "npm start", pkg: script("next start -p $PORT"), want: with("PORT=$PORT")},
		{name: "quoted default", command: "npm start", pkg: script(`next start -p "${PORT:-3000}"`), want: with(`PORT="${PORT:-3000}"`)},
		{name: "port and host", command: "npm start", pkg: script("next start -H 0.0.0.0 -p 8080"), want: with("HOSTNAME=0.0.0.0 PORT=8080")},
		{name: "ipv6 host", command: "npm start", pkg: script("next start --hostname ::"), want: with("HOSTNAME=::")},
		{name: "keep-alive", command: "npm start", pkg: script("next start --keepAliveTimeout 70000"), want: with("KEEP_ALIVE_TIMEOUT=70000")},
		{name: "direct command", command: "next start -p 3000", want: with("PORT=3000")},
		{name: "directory", command: "npm start", pkg: script("next start ./web")},
		{name: "unknown option", command: "npm start", pkg: script("next start --experimental-https")},
		{name: "repeated option", command: "npm start", pkg: script("next start -p 3000 --port 4000")},
		{name: "missing value", command: "npm start", pkg: script("next start -p")},
		{name: "flag as value", command: "npm start", pkg: script("next start -p -H")},
		{name: "short option with equals", command: "npm start", pkg: script("next start -p=3000")},
		{name: "command substitution", command: "npm start", pkg: script("next start -p $(cat port)")},
		{name: "chained command", command: "npm start", pkg: script("next start -p 3000;rm")},
		{name: "glob", command: "npm start", pkg: script("next start -H *")},
		{name: "single quotes", command: "npm start", pkg: script("next start -p '3000'")},
		{name: "forwarded arguments", command: "npm run start -- -p 3000", pkg: script("next start")},
		{name: "lifecycle hook", command: "npm start", pkg: `{"scripts":{"start":"next start -p 3000","prestart":"node m.js"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NextStandaloneStartFor(tt.command, []byte(tt.pkg))
			if ok != (tt.want != nil) || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("NextStandaloneStartFor(%q, %s) = %q, %v; want %q", tt.command, tt.pkg, got, ok, tt.want)
			}
		})
	}
}

func TestNextMajor(t *testing.T) {
	for spec, want := range map[string]int{
		"16.4.6": 16, "^16.0.0": 16, "~16": 16, ">=16.1": 16, "v17.0.0": 17, "15.5.0 || 16": 15,
		"latest": 0, "canary": 0, "": 0, "workspace:*": 0, "npm:next@16": 0,
	} {
		if got := nextMajor(spec); got != want {
			t.Errorf("nextMajor(%q) = %d, want %d", spec, got, want)
		}
	}
}

func TestBunStartNeedsNodePackageManager(t *testing.T) {
	tests := []struct {
		command, pkg string
		want         bool
	}{
		{"bun run start", `{"scripts":{"start":"bun index.ts"}}`, false},
		{"bun start", `{"scripts":{"start":"bunx prisma generate && bun index.ts"}}`, false},
		{"bun index.ts", ``, false},
		{"bun run start", `{"scripts":{"start":"npx prisma migrate deploy && bun src/index.ts"}}`, true},
		{"bun run serve", `{"scripts":{"preserve":"npm run build","serve":"bun x.ts"}}`, true},
		{"bun run start", `{"scripts":{"start":"bun a.ts","poststart":"pnpm notify"}}`, true},
		{"bun run start", `{`, true},
		// A script that runs another script that calls npx.
		{"bun run start", `{"scripts":{"start":"bun run serve","serve":"npx tsx server.ts"}}`, true},
		{"bun start", `{"scripts":{"start":"bun serve","serve":"pnpm exec x"}}`, true},
		{"bun run start", `{"scripts":{"start":"bun run serve","serve":"bun server.ts"}}`, false},
		// Scripts that run each other do not loop.
		{"bun run start", `{"scripts":{"start":"bun run a","a":"bun run start"}}`, false},
		{"bunx serve dist", ``, false},
		// Flags before the script name, and flags with values.
		{"bun run --bun start", `{"scripts":{"start":"npx tsx a.ts"}}`, true},
		{"bun --bun run start", `{"scripts":{"start":"npx tsx a.ts"}}`, true},
		{"bun --cwd . start", `{"scripts":{"start":"npx tsx a.ts"}}`, true},
		{"bun run --bun start", `{"scripts":{"start":"bun a.ts"}}`, false},
		// Script runners call npm.
		{"bun run start", `{"scripts":{"start":"run-p api worker"}}`, true},
		{"bun run start", `{"scripts":{"start":"concurrently \"bun a.ts\" \"bun b.ts\""}}`, true},
		{"bun run start", `{"scripts":{"start":"npm-run-all --parallel a b"}}`, true},
	}
	for _, tt := range tests {
		if got := BunStartNeedsNodePackageManager(tt.command, []byte(tt.pkg)); got != tt.want {
			t.Errorf("%q with %s = %v, want %v", tt.command, tt.pkg, got, tt.want)
		}
	}
}

func TestStartReachesBun(t *testing.T) {
	tests := []struct {
		command, pkg string
		want         bool
	}{
		{"npm start", `{"scripts":{"start":"bun index.ts"}}`, true},
		{"npm run serve", `{"scripts":{"serve":"node a.js","postserve":"bunx x"}}`, true},
		{"pnpm run start", `{"scripts":{"start":"node server.js"}}`, false},
		{"node index.js", ``, false},
		{"npm start", `{`, true},
	}
	for _, tt := range tests {
		if got := StartReachesBun(tt.command, []byte(tt.pkg)); got != tt.want {
			t.Errorf("%q with %s = %v, want %v", tt.command, tt.pkg, got, tt.want)
		}
	}
}

func TestBunHelpers(t *testing.T) {
	for command, want := range map[string]bool{
		"bun run start": true, "bun index.ts": true, "bunx serve dist": true,
		"NODE_ENV=production bun run start": true, "/mise/shims/bun start": true,
		"node index.js": false, "": false, "bundle exec x": false,
	} {
		if got := UsesBun(command); got != want {
			t.Errorf("UsesBun(%q) = %v, want %v", command, got, want)
		}
	}
	if got := BunBinDir("mise/installs/bun/1.3.5/bin/bun"); got != "/mise/installs/bun/1.3.5/bin" {
		t.Fatalf("BunBinDir = %q", got)
	}
	for _, name := range []string{"mise/installs/bun/1.3.5/bin/bunx", "mise/installs/node/24/bin/bun", "mise/installs/bun/bin/bun"} {
		if got := BunBinDir(name); got != "" {
			t.Fatalf("BunBinDir(%q) = %q, want none", name, got)
		}
	}
	if !BunInstallEntry("mise/installs/bun/1.3.5/bin/bun") || BunInstallEntry("mise/installs/bunx") || BunInstallEntry("mise/installs/node/24/bin/node") {
		t.Fatal("BunInstallEntry")
	}
	if got := BunRunnerPath("/mise/installs/bun/1.3.5/bin"); got != "/app/node_modules/.bin:/mise/installs/bun/1.3.5/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" {
		t.Fatalf("BunRunnerPath = %q", got)
	}
}
