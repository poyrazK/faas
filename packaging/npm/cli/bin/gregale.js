#!/usr/bin/env node
// packaging/npm/cli/bin/gregale.js — launcher for the npm channel (ADR-172).
//
// The `gregale` npm package carries no binary. It declares the four
// @gregale/cli-<os>-<arch> packages as optionalDependencies with `os` and
// `cpu` constraints, so npm downloads exactly the one that matches the
// host and skips the rest. This shim finds that package and execs it.
//
// Why a JS shim and not a postinstall that symlinks the binary into place:
// a postinstall is skipped entirely under `npm ci --ignore-scripts`, which
// is what security-conscious CI uses. The shim costs one node startup
// (~30 ms) and always works.

"use strict";

const { spawnSync } = require("node:child_process");

// process.arch uses node's names (x64); release artifacts use Go's
// (amd64). This table is the only place the two vocabularies meet.
const TARGETS = {
	"darwin-arm64": "@gregale/cli-darwin-arm64",
	"darwin-x64": "@gregale/cli-darwin-amd64",
	"linux-arm64": "@gregale/cli-linux-arm64",
	"linux-x64": "@gregale/cli-linux-amd64",
};

function fail(message) {
	process.stderr.write(`gregale: ${message}\n`);
	process.exit(1);
}

function resolveBinary() {
	const key = `${process.platform}-${process.arch}`;
	const pkg = TARGETS[key];
	if (!pkg) {
		// Windows lands here only if npm's `os` gate was bypassed
		// (--force). See ADR-172: cmd/gregale imports pkg/fcvm, which
		// is unix-only.
		fail(
			`unsupported platform ${key}. gregale ships darwin and linux on ` +
				`amd64/arm64. Install from https://get.gregale.dev if you believe ` +
				`this is wrong.`,
		);
	}
	try {
		return require.resolve(`${pkg}/bin/gregale`);
	} catch {
		fail(
			`the platform package ${pkg} is missing.\n` +
				`This usually means npm skipped optional dependencies. Reinstall with:\n` +
				`  npm install --include=optional gregale\n` +
				`or install directly:  curl -fsSL https://get.gregale.dev | sh`,
		);
	}
}

const result = spawnSync(resolveBinary(), process.argv.slice(2), {
	stdio: "inherit",
});

if (result.error) {
	fail(`could not run the gregale binary: ${result.error.message}`);
}

// Re-raise the child's signal so callers see a real signal death rather
// than a synthesised exit code; `gregale logs -f` interrupted with Ctrl-C
// should look interrupted to the shell.
if (result.signal) {
	process.kill(process.pid, result.signal);
} else {
	process.exit(result.status === null ? 1 : result.status);
}
