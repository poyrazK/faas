#!/bin/sh
# finish-install.sh — runs inside the agent-base build right after `npm ci`
# (images/agent-base.Dockerfile). It proves every harness binary came from
# images/agent-base/package-lock.json, drops the OpenCode builds this CPU
# and libc never run, links the binaries into /usr/local/bin, and checks
# each version against the preset catalog.
set -eu

# The defaults are the image paths; images/agent_base_install_test.py points
# them at a fixture tree.
modules=${AGENT_BASE_MODULES:-/opt/gregale/agents/node_modules}
bindir=${AGENT_BASE_BINDIR:-/usr/local/bin}
catalog=${AGENT_BASE_CATALOG:-/usr/share/faas/agent-presets.json}

# OpenCode's postinstall links the variant that runs here into
# bin/opencode.exe. When no installed variant runs, it falls back to an
# `npm install` from the registry, outside the lockfile; refuse that result.
# npm installs four linux-x64 variants on musl (no libc field on two of
# them), about 192 MB each, so the three it did not pick are removed.
selected=""
for dir in "$modules"/opencode-linux-*; do
  if cmp -s "$modules/opencode-ai/bin/opencode.exe" "$dir/bin/opencode"; then
    selected=$dir
  fi
done
if [ -z "$selected" ]; then
  echo "agent-base: opencode-ai/bin/opencode.exe matches no locked variant" >&2
  exit 1
fi
for dir in "$modules"/opencode-linux-*; do
  if [ "$dir" != "$selected" ]; then
    rm -rf "$dir"
  fi
done

# Claude Code's installer links its platform package's binary the same way,
# with no registry fallback. The binary is Anthropic's published file,
# unmodified (ADR-20261027090010 §2); check that the link holds it.
set -- "$modules"/@anthropic-ai/claude-code-linux-*
if [ "$#" -ne 1 ] || ! cmp -s "$modules/@anthropic-ai/claude-code/bin/claude.exe" "$1/claude"; then
  echo "agent-base: claude-code/bin/claude.exe is not the locked platform binary" >&2
  exit 1
fi

for bin in codex opencode claude; do
  ln -sf "$modules/.bin/$bin" "$bindir/$bin"
done

# Each preset reports the version the catalog pins.
python3 -I - "$catalog" "$bindir" <<'EOF'
import json, os, subprocess, sys

catalog = json.load(open(sys.argv[1]))
bindir = sys.argv[2]
env = {"PATH": bindir + ":/usr/bin:/bin", "HOME": "/tmp/agent-base-version-check"}
for preset in catalog["presets"]:
    env_preset = dict(env, **preset.get("env", {}))
    binary = os.path.join(bindir, os.path.basename(preset["binary"]))
    out = subprocess.run([binary, "--version"], env=env_preset, capture_output=True,
                         text=True, timeout=60)
    text = out.stdout + out.stderr
    if out.returncode != 0 or preset["version"] not in text:
        sys.exit(f"agent-base: {preset['name']} --version printed {text.strip()!r}, want {preset['version']}")
    print(f"agent-base: {preset['name']} {preset['version']} ok")
EOF
rm -rf /tmp/agent-base-version-check
