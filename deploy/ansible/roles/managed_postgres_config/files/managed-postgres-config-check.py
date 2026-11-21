#!/usr/bin/env python3
"""Check a managed PostgreSQL provider config before apid loads it.

Usage: managed-postgres-config-check.py CONFIG [--catalog-dsn DSN]
       managed-postgres-config-check.py CONFIG --fingerprints

It refuses a config that would strand an existing database: apid resolves a
database only through the backend ID and fingerprint the catalog recorded at
placement (pkg/managedpostgres/registry.go Resolve). The fingerprint is
sha256 over {"driver","region","namespace","settings"} as Go's json.Marshal
writes it; previous_settings add accepted fingerprints. It also refuses
settings that look like secrets: those belong in sealed.env through
secret_env.

With --catalog-dsn it reads every non-deleted database's (backend_id,
backend_fingerprint) through psql and fails unless each resolves. Exit 0 on
success; otherwise prints why and exits 1.
"""
import hashlib
import json
import re
import subprocess
import sys

SECRET_KEY = re.compile(r"(key|secret|token|password|credential)", re.IGNORECASE)
ENV_NAME = re.compile(r"^[A-Z][A-Z0-9_]{0,127}$")


def go_json(value):
    """Encode like Go's json.Marshal: compact, sorted map keys, UTF-8, HTML-escaped."""
    text = json.dumps(value, separators=(",", ":"), ensure_ascii=False, sort_keys=True)
    for raw, escaped in (("<", "\\u003c"), (">", "\\u003e"), ("&", "\\u0026"),
                         (" ", "\\u2028"), (" ", "\\u2029")):
        text = text.replace(raw, escaped)
    return text


def fingerprint(backend, settings):
    # Field order matters: Go marshals the struct fields in declaration order.
    payload = ('{"driver":' + go_json(backend.get("driver", "")) +
               ',"region":' + go_json(backend.get("region", "")) +
               ',"namespace":' + go_json(backend.get("namespace", "")) +
               ',"settings":' + go_json(settings) + "}")
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def accepted(config):
    """Map backend ID to the fingerprints apid resolves for it."""
    out = {}
    for backend in config.get("backends") or []:
        prints = {fingerprint(backend, backend.get("settings"))}
        for previous in backend.get("previous_settings") or []:
            prints.add(fingerprint(backend, previous))
        out[backend.get("id", "")] = prints
    return out


def problems(config):
    errs = []
    if not isinstance(config.get("default_region"), str) or not config["default_region"]:
        errs.append("default_region is empty")
    backends = config.get("backends") or []
    if not backends:
        errs.append("backends is empty")
    ids = set()
    for backend in backends:
        bid = backend.get("id", "")
        for field in ("id", "driver", "region"):
            if not backend.get(field):
                errs.append(f"backend {bid or '?'}: {field} is empty")
        if bid in ids:
            errs.append(f"backend {bid}: listed twice")
        ids.add(bid)
        for key, value in (backend.get("settings") or {}).items():
            if not isinstance(value, str):
                # apid decodes settings as strings; an unquoted YAML number
                # would stop it from starting.
                errs.append(f"backend {bid}: setting {key} must be a string (quote it in YAML)")
            if SECRET_KEY.search(key):
                errs.append(f"backend {bid}: setting {key} looks like a secret; put it in sealed.env and name it in secret_env")
        for name, env in (backend.get("secret_env") or {}).items():
            if not ENV_NAME.match(str(env)):
                errs.append(f"backend {bid}: secret_env {name} must name an environment variable, not hold a value")
    defaults = config.get("defaults") or {}
    if config.get("default_region") and config["default_region"] not in defaults:
        errs.append("defaults has no backend for default_region")
    for region, bid in defaults.items():
        if bid not in ids:
            errs.append(f"defaults: region {region} names unknown backend {bid}")
    return errs


def catalog_placements(dsn):
    out = subprocess.run(
        ["psql", "-X", "-A", "-t", "-F", " ", dsn, "-c",
         "SELECT DISTINCT backend_id, backend_fingerprint FROM managed_postgres_databases WHERE state <> 'deleted'"],
        check=True, capture_output=True, text=True).stdout
    return [tuple(line.split(" ", 1)) for line in out.splitlines() if line.strip()]


def main(argv):
    if len(argv) == 3 and argv[2] == "--fingerprints":
        # Prints "<backend id> <fingerprint>" per accepted fingerprint; the Go
        # test TestAnsibleConfigCheckFingerprintsMatch pins it to registry.go.
        with open(argv[1], encoding="utf-8") as handle:
            for bid, prints in sorted(accepted(json.load(handle)).items()):
                for fp in sorted(prints):
                    print(bid, fp)
        return 0
    if len(argv) not in (2, 4) or (len(argv) == 4 and argv[2] != "--catalog-dsn"):
        print(__doc__, file=sys.stderr)
        return 2
    with open(argv[1], encoding="utf-8") as handle:
        config = json.load(handle)
    errs = problems(config)
    if len(argv) == 4 and not errs:
        prints = accepted(config)
        try:
            placements = catalog_placements(argv[3])
        except (OSError, subprocess.CalledProcessError) as exc:
            detail = getattr(exc, "stderr", "") or str(exc)
            errs.append(f"cannot read the catalog's placements: {detail.strip()}")
            placements = []
        for bid, fp in placements:
            if fp not in prints.get(bid, set()):
                errs.append(f"databases placed on backend {bid} (fingerprint {fp[:12]}…) would not resolve: "
                            "driver, region, namespace and settings must match what they were created with "
                            "(or list the old settings in previous_settings)")
    for err in errs:
        print(f"managed postgres config: {err}", file=sys.stderr)
    return 1 if errs else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
