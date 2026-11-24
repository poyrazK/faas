package sharedruntime

import (
	"encoding/json"
	"errors"
	"strings"
)

// Next.js standalone output (ADR-20261124160000). A Next.js app started with
// `next start` needs its whole node_modules, which is about 340 MB for a small
// app: more than the Free plan's 256 MB app layer. `output: "standalone"`
// makes `next build` trace the files the server needs into
// .next/standalone (about 40 MB). The builder turns it on through Next's
// build adapter hook, and imaged then ships only that tree.

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

// nextAdapter sets output "standalone" for `next build` unless the app's
// config chose an output itself. NEXT_ADAPTER_PATH is read by Next 15.5 and
// later; modifyConfig runs before the build reads the config.
const nextAdapter = `"use strict";
// Written by Gregale: builds with output "standalone" so the deployed app
// holds only the files its server needs.
module.exports = {
  name: "gregale-standalone",
  modifyConfig(config, ctx) {
    if (ctx && ctx.phase === "phase-production-build" && !config.output) {
      config.output = "standalone";
    }
    return config;
  },
};
`

// nextStandaloneCopy puts the static assets and public files next to the
// standalone server, as Next's deployment docs do. It changes nothing when
// the build wrote no standalone server (the app chose another output). It
// never fails the build: a copy that fails leaves no marker, and imaged then
// ships the app with its node_modules.
const nextStandaloneCopy = `sh -c 'if [ -f .next/standalone/server.js ] && ` +
	`rm -rf .next/standalone/.next/static && mkdir -p .next/standalone/.next && ` +
	`{ [ ! -d .next/static ] || cp -R .next/static .next/standalone/.next/static; } && ` +
	`{ [ ! -d public ] || { rm -rf .next/standalone/public && cp -R public .next/standalone/public; }; }; then ` +
	`: > .next/standalone/` + nextStandaloneMarkerName + `; fi; true'`

// nextStandaloneMarkerName is the file the copy step writes last. imaged ships
// the standalone tree only when it is there: a standalone server built
// without the copy (the app's own output setting, an older builder, the
// switch off) lacks its static and public files.
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

// ErrNotNextStandalone reports a plan or package the standalone build does
// not apply to.
var ErrNotNextStandalone = errors.New("not a next start app")

// StampNextStandalone rewrites a Railpack plan so its build step builds a
// Next.js app with standalone output. It applies only to an app whose
// package.json depends on Next 16 or later, builds with `next build` and
// starts with a plain `next start` through the package manager; anything
// else returns ErrNotNextStandalone and the plan is left as it is. Deploy
// inputs are unchanged: imaged chooses the standalone tree when the build
// wrote it.
func StampNextStandalone(planJSON, packageJSON []byte) ([]byte, error) {
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
		"customName": "copy static files into the standalone server",
		"cmd":        nextStandaloneCopy,
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
