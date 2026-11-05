#!/usr/bin/env python3
"""Fail fast when capability flags hide what a native acceptance run needs.

Native acceptance runs exercise preview and internal capabilities through a
deployed API with an ordinary account token. Those capabilities are off by
default (ADR-20261023115741), so an account nobody released them to meets
404s from routes the gate hides, deep into a long run. This reads
GET /v1/capabilities for the token's account and, when a required capability
or one of its ancestors is hidden, prints the operator commands that release
them and exits 1. An apid without the capability gate reports nothing hidden,
so the check passes.

  require-capabilities.py --api URL --token-env NAME CAPABILITY...
"""
import argparse
import json
import os
from pathlib import Path
import sys
import urllib.request

CATALOG = Path(__file__).resolve().parents[2] / "pkg" / "productcap" / "catalog.json"


def load_parents(path=CATALOG):
    """Return each catalog capability id mapped to its parent ('' for none)."""
    capabilities = json.loads(Path(path).read_text())["capabilities"]
    return {c["id"]: c.get("parent", "") for c in capabilities}


def with_ancestors(required, parents):
    """Return required plus every ancestor, ancestors first, each once."""
    ordered = []
    for capability in required:
        chain = []
        while capability and capability not in ordered and capability not in chain:
            chain.append(capability)
            capability = parents.get(capability, "")
        ordered.extend(reversed(chain))
    return ordered


def get(api, token, path):
    request = urllib.request.Request(api.rstrip("/") + path, headers={
        "Authorization": "Bearer " + token, "Accept": "application/json"})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def check(api, token, required, parents):
    """Return the operator commands that would release what is hidden."""
    unknown = [c for c in required if c not in parents]
    if unknown:
        raise SystemExit("require-capabilities: not in pkg/productcap/catalog.json: " + ", ".join(unknown))
    hidden = set(get(api, token, "/v1/capabilities").get("hidden_capabilities") or [])
    missing = [c for c in with_ancestors(required, parents) if c in hidden]
    if not missing:
        return []
    account = get(api, token, "/v1/account").get("id") or "ACCOUNT_ID"
    return [f'gregalectl flags allow --capability {c} --account-id {account} --reason "native acceptance account" --yes'
            for c in missing]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--api", required=True, help="the deployment's public API URL")
    parser.add_argument("--token-env", required=True, help="environment variable holding the account token")
    parser.add_argument("capability", nargs="+", help="catalog capability ids the run exercises")
    args = parser.parse_args(argv)
    token = os.environ.get(args.token_env, "")
    if not token:
        raise SystemExit(f"require-capabilities: ${args.token_env} is empty")
    commands = check(args.api, token, args.capability, load_parents())
    if commands:
        print("require-capabilities: capability flags hide what this run needs from its account; "
              "an operator must release them first:\n  " + "\n  ".join(commands), file=sys.stderr)
        return 1
    print(json.dumps({"capabilities": "released", "checked": args.capability}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
