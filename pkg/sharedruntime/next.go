package sharedruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Next.js standalone output (ADR-20261124160000). A Next.js app started with
// `next start` needs its whole node_modules, which is about 340 MB for a small
// app: more than the Free plan's 256 MB app layer. `output: "standalone"`
// makes `next build` trace the files the server needs into
// .next/standalone (about 40 MB). The builder turns it on through Next's
// build adapter hook and keeps the tree only when the whole app is larger
// than the plan's app layer cap; imaged then ships only that tree.

// NextStandaloneEnv turns the standalone build of Next.js apps off on a
// builder node when "0". On by default.
const NextStandaloneEnv = "FAAS_NEXT_STANDALONE"

// NextStandaloneEnabled reports whether a builder node builds Next.js apps
// with standalone output, given its value of NextStandaloneEnv.
func NextStandaloneEnabled(value string) bool { return value != "0" }

// NextStandaloneServer is the standalone server's tar path in a build
// artifact. Next writes it only for output "standalone".
const NextStandaloneServer = "app/.next/standalone/server.js"

// NextStandalonePrefix is the tar path of the standalone tree.
const NextStandalonePrefix = "app/.next/standalone"

// NextStandaloneStart runs the standalone server. server.js changes to its
// own directory, reads PORT, and listens on HOSTNAME.
var NextStandaloneStart = []string{"/bin/sh", "-c", "exec node /app/.next/standalone/server.js"}

// NextStandaloneEnvVars lists what the standalone server needs beyond the
// image's environment: without HOSTNAME it listens on the machine's host
// name, not on every interface.
var NextStandaloneEnvVars = map[string]string{"HOSTNAME": "0.0.0.0"}

// nextAdapterPath is where the build step writes the adapter. /tmp is not
// part of any deploy input.
const nextAdapterPath = "/tmp/gregale-next-standalone-adapter.cjs"

const nextAdapterAsset = "gregale-next-standalone-adapter"

// nextAdapterSetFlag is the file the adapter writes when it set the output
// itself. The post-build step acts only then, so an app that chose output
// "standalone" in its own config keeps its build as it is.
const nextAdapterSetFlag = "/tmp/gregale-next-standalone-set"

// nextAdapter sets output "standalone" for `next build` unless the app's
// config chose an output itself. NEXT_ADAPTER_PATH is read by Next 15.5 and
// later; modifyConfig runs before the build reads the config.
const nextAdapter = `"use strict";
// Written by Gregale: builds with output "standalone" so an app too large
// for its plan's app layer can ship only the files its server needs.
module.exports = {
  name: "gregale-standalone",
  modifyConfig(config, ctx) {
    if (ctx && ctx.phase === "phase-production-build" && !config.output) {
      config.output = "standalone";
      require("fs").writeFileSync("` + nextAdapterSetFlag + `", "");
    }
    return config;
  },
};
`

// nextStandaloneStep (a format taking the reserve, the cap in bytes and
// the cap in MB) keeps the standalone
// tree only when the app needs it. After the build it estimates the ext4
// image the full /app would need, the way imaged sizes an app layer
// (rootfs.CheckCapForStaging): the files' apparent bytes, plus 10 % slack
// and 60 % more for the share of files under 4 KiB, and at least 16 MiB of
// headroom. node_modules is mostly small files, so its image is far larger
// than its bytes. The standalone tree and the build caches (cache mounts,
// never deployed) are left out, and nextLayerReserveBytes is added for
// what else the layer carries. An app that fits ships whole, as before, and
// the tree is removed. A larger one gets its static assets and public
// files copied next to the standalone server, as Next's deployment docs do,
// and the marker written last. The step acts only when the adapter set the
// output, prints its estimate and choice to the build log, and never fails
// the build: without the marker imaged ships the whole app.
const nextStandaloneStep = `sh -c 'if [ -f ` + nextAdapterSetFlag + ` ] && [ -f .next/standalone/server.js ]; then ` +
	`f() { find . \( -path ./.next/standalone -o -path ./.next/cache -o -path ./node_modules/.cache \) -prune -o -type f "$@"; }; keep=0; ` +
	`n=$(( $(f -print | wc -l) )); s=$(( $(f -size -4096c -print | wc -l) )); b=$(( $(f -exec cat {} + | wc -c) )); ` +
	`[ "$n" -gt 0 ] || n=1; e=$(( b * (110 * n + 60 * s) / (100 * n) )); m=$(( b + 16777216 )); [ "$m" -le "$e" ] || e=$m; ` +
	`c=$(( $(find .next/cache node_modules/.cache -type f -exec cat {} + 2>/dev/null | wc -c) )); ` +
	`echo "Next.js: the full app needs about $(( (e + %[1]d) >> 20 )) MB of the plan cap of %[3]d MB ($n files, build caches of $(( c >> 20 )) MB not counted)"; ` +
	`if [ $(( e + %[1]d )) -gt %[2]d ] && rm -rf .next/standalone/.next/static && mkdir -p .next/standalone/.next && ` +
	`{ [ ! -d .next/static ] || cp -R .next/static .next/standalone/.next/static; } && ` +
	`{ [ ! -d public ] || { rm -rf .next/standalone/public && cp -R public .next/standalone/public; }; }; then ` +
	`: > .next/standalone/` + nextStandaloneMarkerName + ` && keep=1; fi; ` +
	`if [ "$keep" = 1 ]; then echo "Next.js: shipping the standalone server"; else rm -rf .next/standalone; echo "Next.js: shipping the full app"; fi; fi; true'`

// nextLayerReserveBytes is what an app layer holds beyond /app: guest-init
// and the directories above /app.
const nextLayerReserveBytes = 24 << 20

// nextStandaloneCommand is nextStandaloneStep for a cap of capMB.
func nextStandaloneCommand(capMB int) string {
	return fmt.Sprintf(nextStandaloneStep, nextLayerReserveBytes, int64(capMB)<<20, capMB)
}

// nextStandaloneMarkerName is the file nextStandaloneStep writes last.
// imaged ships the standalone tree only when it is there: a standalone
// server built without the step (the app's own output setting, an older
// builder, the switch off) lacks its static and public files.
const nextStandaloneMarkerName = ".gregale-standalone"

// NextStandaloneMarker is the marker's tar path.
const NextStandaloneMarker = NextStandalonePrefix + "/" + nextStandaloneMarkerName

// nextBuildCommands are the commands Railpack plans for a build script.
var nextBuildCommands = map[string]bool{
	"npm run build": true, "pnpm run build": true, "yarn run build": true, "bun run build": true,
}

// nextMinMajor is the first Next.js major whose adapter hook the builder
// relies on.
const nextMinMajor = 16

type nextPackage struct {
	Scripts      map[string]string `json:"scripts"`
	Dependencies map[string]string `json:"dependencies"`
}

// nextStartScript reports whether package.json starts the app with a plain
// `next start` and no npm lifecycle hooks around it.
func (p nextPackage) nextStartScript() bool {
	return strings.TrimSpace(p.Scripts["start"]) == "next start" &&
		p.Scripts["prestart"] == "" && p.Scripts["poststart"] == ""
}

// NextStart reports whether command starts the app's Next.js server the
// way `next start` does: the command itself, or a package manager running a
// start script that is exactly `next start`. Arguments, other scripts and
// lifecycle hooks are not the plain server and keep the app's own start.
func NextStart(command string, packageJSON []byte) bool {
	fields := strings.Fields(command)
	if len(fields) == 2 && fields[0] == "next" && fields[1] == "start" {
		return true
	}
	if len(fields) < 2 {
		return false
	}
	switch fields[0] {
	case "npm", "pnpm", "yarn", "bun":
	default:
		return false
	}
	start := len(fields) == 2 && fields[1] == "start" ||
		len(fields) == 3 && (fields[1] == "run" || fields[1] == "run-script") && fields[2] == "start"
	if !start {
		return false
	}
	var pkg nextPackage
	if json.Unmarshal(packageJSON, &pkg) != nil {
		return false
	}
	return pkg.nextStartScript()
}

// nextMajor returns the lowest major version a dependency spec such as
// "16.4.6", "^16.0.0" or "~16" allows, or 0 when it cannot tell.
func nextMajor(spec string) int {
	spec = strings.TrimSpace(spec)
	if i := strings.Index(spec, "||"); i >= 0 {
		spec = strings.TrimSpace(spec[:i])
	}
	spec = strings.TrimLeft(spec, "^~>=v ")
	major := 0
	for i := 0; i < len(spec) && spec[i] >= '0' && spec[i] <= '9'; i++ {
		major = major*10 + int(spec[i]-'0')
		if major > 1000 {
			return 0
		}
	}
	return major
}

// ReasonNextStandaloneReread: imaged could neither select a Next.js
// standalone tree nor re-read the app layer it filtered, so the app keeps
// every layer above the runner base.
const ReasonNextStandaloneReread = "next_standalone_reread"

// ErrNotNextStandalone reports a plan or package the standalone build does
// not apply to.
var ErrNotNextStandalone = errors.New("not a next start app")

// StampNextStandalone rewrites a Railpack plan so its build step builds a
// Next.js app with standalone output. It applies only to an app whose
// package.json depends on Next 16 or later, builds with `next build` and
// starts with a plain `next start` through the package manager; anything
// else returns ErrNotNextStandalone and the plan is left as it is. capMB is
// the plan's app layer cap: the build keeps the standalone tree only for an
// app larger than that. Deploy inputs are unchanged: imaged chooses the
// standalone tree when the build marked it.
func StampNextStandalone(planJSON, packageJSON []byte, capMB int) ([]byte, error) {
	if capMB <= 0 {
		return nil, ErrNotNextStandalone
	}
	var pkg nextPackage
	if err := json.Unmarshal(packageJSON, &pkg); err != nil {
		return nil, ErrNotNextStandalone
	}
	if nextMajor(pkg.Dependencies["next"]) < nextMinMajor || !pkg.nextStartScript() ||
		!strings.Contains(pkg.Scripts["build"], "next build") {
		return nil, ErrNotNextStandalone
	}
	var plan map[string]any
	if err := json.Unmarshal(planJSON, &plan); err != nil || plan == nil {
		return nil, errors.New("invalid Railpack plan")
	}
	deploy, _ := plan["deploy"].(map[string]any)
	if deploy == nil {
		return nil, errors.New("invalid Railpack deployment")
	}
	if start, _ := deploy["startCommand"].(string); !NextStart(start, packageJSON) {
		return nil, ErrNotNextStandalone
	}
	steps, _ := plan["steps"].([]any)
	var build map[string]any
	for _, raw := range steps {
		if step, ok := raw.(map[string]any); ok && step["name"] == "build" {
			build = step
			break
		}
	}
	if build == nil {
		return nil, ErrNotNextStandalone
	}
	commands, ok := build["commands"].([]any)
	if !ok || len(commands) == 0 {
		return nil, ErrNotNextStandalone
	}
	assets, err := objectField(build, "assets")
	if err != nil {
		return nil, err
	}
	if _, stamped := assets[nextAdapterAsset]; stamped {
		return nil, ErrNotNextStandalone
	}
	stamped := make([]any, 0, len(commands)+2)
	stamped = append(stamped, map[string]any{
		"customName": "write the standalone build adapter",
		"name":       nextAdapterAsset,
		"path":       nextAdapterPath,
	})
	// NEXT_ADAPTER_PATH goes on the build command alone. A step variable
	// would reach the image's environment, where `next start` would look
	// for an adapter that is not in the deployed app.
	rewritten := false
	for _, raw := range commands {
		command, ok := raw.(map[string]any)
		cmd, _ := command["cmd"].(string)
		if ok && nextBuildCommands[cmd] {
			copied := make(map[string]any, len(command))
			for k, v := range command {
				copied[k] = v
			}
			copied["cmd"] = "env NEXT_ADAPTER_PATH=" + nextAdapterPath + " " + cmd
			raw, rewritten = copied, true
		}
		stamped = append(stamped, raw)
	}
	if !rewritten {
		return nil, ErrNotNextStandalone
	}
	assets[nextAdapterAsset] = nextAdapter
	stamped = append(stamped, map[string]any{
		"customName": "keep the standalone server if the app needs it",
		"cmd":        nextStandaloneCommand(capMB),
	})
	build["commands"] = stamped
	return json.Marshal(plan)
}

// objectField returns object[key] as a JSON object, adding an empty one when
// it is absent.
func objectField(object map[string]any, key string) (map[string]any, error) {
	switch v := object[key].(type) {
	case nil:
		m := map[string]any{}
		object[key] = m
		return m, nil
	case map[string]any:
		return v, nil
	default:
		return nil, errors.New("invalid Railpack step " + key)
	}
}
