#!/usr/bin/env python3
"""Check that the Debian parent base is a layer prefix of its children (ADR-053).

imaged stages runner-node24 and runner-python312 as a delta over the staged
base-debian-parent ext4 (pkg/imaged/base_stage.go, ensureBaseExt4ParentRef):
the parent image's layers must be the first layers of each child image, or
oci.LayersAboveBase refuses the chain and the node cannot stage the child.
The images are built FROM digest-pinned upstreams in images/Dockerfile.lock,
so the rule holds exactly when each child's upstream is built on the parent's
Debian snapshot. Bumping one digest without the others breaks it while every
image still builds and scans cleanly on its own.

The child list is read from DefaultRuntimeBaseRefs, the rows whose ParentRef
is BaseRefDebianParent. Needs anonymous registry access.
"""

from __future__ import annotations

import json
import pathlib
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[2]
PARENT_DOCKERFILE = "images/base-debian-parent.Dockerfile"
ACCEPT = ", ".join([
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
])


def child_dockerfiles(base_stage_go: str) -> list[str]:
    """Dockerfiles of the runtimes DefaultRuntimeBaseRefs stages over the Debian parent."""
    runtimes = re.findall(r"\{Runtime:\s*Runtime(\w+),[^}]*ParentRef:\s*BaseRefDebianParent\}", base_stage_go)
    return [f"images/runner-{name.lower()}.Dockerfile" for name in runtimes]


def pinned_ref(lock: dict, dockerfile: str) -> str:
    entries = [e for e in lock["pinned"] if e["dockerfile"] == dockerfile]
    if len(entries) != 1:
        raise ValueError(f"{dockerfile}: want exactly one lock entry, found {len(entries)}")
    return f"{entries[0]['resolved_repo']}@{entries[0]['digest']}"


def chain_problems(lock: dict, children: list[str], diff_ids) -> list[str]:
    parent_ref = pinned_ref(lock, PARENT_DOCKERFILE)
    parent = diff_ids(parent_ref)
    problems = []
    for dockerfile in children:
        ref = pinned_ref(lock, dockerfile)
        child = diff_ids(ref)
        if child[:len(parent)] != parent:
            problems.append(
                f"{dockerfile} pins {ref}, whose first layers {child[:len(parent)]} are not "
                f"{PARENT_DOCKERFILE}'s {parent_ref} layers {parent}")
    return problems


def _fetch(url: str, accept: str, token: str | None = None) -> bytes:
    request = urllib.request.Request(url, headers={"Accept": accept})
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return response.read()
    except urllib.error.HTTPError as err:
        challenge = err.headers.get("WWW-Authenticate", "")
        if err.code != 401 or token or not challenge.startswith("Bearer "):
            raise
        params = dict(part.split("=", 1) for part in challenge[len("Bearer "):].split(","))
        params = {key: value.strip('"') for key, value in params.items()}
        realm = params.pop("realm")
        with urllib.request.urlopen(realm + "?" + urllib.parse.urlencode(params), timeout=60) as response:
            body = json.load(response)
        return _fetch(url, accept, body.get("token") or body.get("access_token"))


def registry_diff_ids(ref: str) -> list[str]:
    repo, digest = ref.split("@", 1)
    host, path = repo.split("/", 1)
    base = f"https://{host}/v2/{path}"
    manifest = json.loads(_fetch(f"{base}/manifests/{digest}", ACCEPT))
    if "manifests" in manifest:
        child = next(m for m in manifest["manifests"]
                     if m.get("platform", {}).get("os") == "linux"
                     and m["platform"].get("architecture") == "amd64")
        manifest = json.loads(_fetch(f"{base}/manifests/{child['digest']}", ACCEPT))
    config = json.loads(_fetch(f"{base}/blobs/{manifest['config']['digest']}", "*/*"))
    return config["rootfs"]["diff_ids"]


def main() -> int:
    lock = json.loads((ROOT / "images/Dockerfile.lock").read_text())
    children = child_dockerfiles((ROOT / "pkg/imaged/base_stage.go").read_text())
    if not children:
        print("parent-base-chain: no ParentRef rows found in pkg/imaged/base_stage.go", file=sys.stderr)
        return 1
    problems = chain_problems(lock, children, registry_diff_ids)
    if problems:
        print("parent-base-chain: imaged cannot stage these runtimes over the Debian parent (ADR-053); "
              "pin them to upstream digests built on the parent's Debian snapshot:", file=sys.stderr)
        for problem in problems:
            print(f"  {problem}", file=sys.stderr)
        return 1
    print(f"parent-base-chain: OK ({', '.join(children)} build on {PARENT_DOCKERFILE})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
