#!/usr/bin/env bash
# check_no_incompatible_deps.sh — fail a PR that pulls a +incompatible
# version of a *direct* go.mod require that is not on the
# documented allowlist.
#
# Why this exists
# ---------------
# A `+incompatible` suffix on a module version means Go is fetching it
# at a non-semver import path (the module is either pre-modules or
# refuses to adopt a /vN major suffix). It's a tripwire: the module
# author is signalling "I will not promise anything about backwards
# compatibility" — and Go will never auto-bump past it.
#
# Today this flags exactly one direct dep:
#   github.com/stripe/stripe-go v70.15.0+incompatible
# (go.mod:25). v70 pre-dates the modern lookup_key primitive on
# PlanParams (v74+). A future major bump will require touching every
# call site that uses lookup-by-lookup_key — fine, but the bump must
# happen deliberately, not because Dependabot decided to. The gate
# below makes new hits visible at PR time, and prevents a second
# +incompatible dep from ever slipping in unannounced.
#
# Allowlist mechanism
# -------------------
# A module on the allowlist is *named* (path only, no version) so it
# survives a Dependabot-driven patch bump to the same module. The
# allowlist is read from FAAS_INCOMPATIBLE_DEPS_ALLOWLIST (colon-
# separated paths). Setting it to an empty string lists nothing; the
# gate then fails on any +incompatible direct dep. The default
# (unset) is the historical "fail closed" allowlist of one entry —
# the known stripe-go hit. To turn the gate red on stripe-go, run:
#   FAAS_INCOMPATIBLE_DEPS_ALLOWLIST="" bash .../check_no_incompatible_deps.sh
#
# Why allowlist at all: the alternative is to keep the gate RED
# forever (until someone does the multi-day stripe-go-v80 upgrade).
# CI being permanently red on a single step blocks every other PR's
# review signal and the no-direct-push workflow guard becomes
# advisory. The allowlist lets the tripwire stay *armed* for new
# +incompatible direct deps but lets the existing one pass with
# the operator-visible "tracked" annotation.
#
# Indirect deps are intentionally allowed: a transitive consumer may
# pin a +incompatible version and we cannot upgrade it without
# bumping the consumer, which is out of scope for a "go.mod hygiene"
# gate. Only direct requires are load-bearing for the release
# invariant.
#
# This is pure bash: no `pip install`, runner setup, or Go-built binary.

set -euo pipefail

# `go list -m -f '{{if not .Indirect}}{{.Path}} {{.Version}}{{end}}'`
# emits the (path, version) tuple for direct deps only. `grep` against
# `+incompatible` matches the suffix on the version. The `|| true`
# ensures grep returns 0 when there are no matches (it doesn't change
# the script's overall exit code thanks to `set -e` not triggering on
# commands followed by `||`).
hits=$(go list -m -f '{{if not .Indirect}}{{.Path}} {{.Version}}{{end}}' all \
       | grep '+incompatible' || true)

# Default allowlist: the known stripe-go v70 hit. Setting the env
# var to "" means "allow nothing" (fail on any +incompatible direct
# dep). Unsetting the env var falls back to the default. The CI
# workflow does not pass this env var, so the workflow runs with
# the default — the gate stays armed for new direct deps.
if [ "${FAAS_INCOMPATIBLE_DEPS_ALLOWLIST+x}" = "x" ]; then
  allowlist="$FAAS_INCOMPATIBLE_DEPS_ALLOWLIST"
else
  allowlist="github.com/stripe/stripe-go"
fi

# Filter hits against the allowlist. Each allowed path is matched
# as a prefix of the (path, version) line so leading/trailing whitespace
# is handled. Comments are stripped on the fly.
untracked=""
if [ -n "$hits" ]; then
  while IFS= read -r hit; do
    [ -z "$hit" ] && continue
    path="${hit% *}"  # strip the version suffix
    allowed=0
    # Skip allowlist check when the allowlist is empty (env var
    # explicitly set to ""). The else-branch — every hit is untracked.
    if [ -z "$allowlist" ]; then
      allowed=0
    else
      IFS=':' read -r -a entries <<< "$allowlist"
      for entry in "${entries[@]}"; do
        [ -z "$entry" ] && continue
        if [ "$entry" = "$path" ]; then
          allowed=1
          break
        fi
      done
    fi
    if [ "$allowed" -eq 0 ]; then
      untracked="${untracked}${hit}\n"
    else
      echo "::warning:: allowlisted +incompatible direct dep: ${hit} (tracked: see FAAS_INCOMPATIBLE_DEPS_ALLOWLIST)" >&2
    fi
  done <<< "$hits"
fi

if [ -n "$untracked" ]; then
  echo "::error:: direct go.mod require uses a +incompatible version (NOT on FAAS_INCOMPATIBLE_DEPS_ALLOWLIST):" >&2
  printf "%b" "$untracked" >&2
  echo "::error:: +incompatible means the module is at a non-semver path and Go will never auto-bump it. The release gate is: no direct require may carry this suffix unless added to the allowlist. To add: append the module path to FAAS_INCOMPATIBLE_DEPS_ALLOWLIST (colon-separated) in .github/workflows/ci.yml. The current allowlist is: ${allowlist}" >&2
  exit 1
fi
