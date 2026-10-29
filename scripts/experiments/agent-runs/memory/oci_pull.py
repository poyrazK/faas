#!/usr/bin/env python3
"""Pull one pinned linux/amd64 image into a directory, without docker.

usage: oci_pull.py REGISTRY REPO DIGEST DEST

Blobs are verified against their digests. Layers are unpacked in order with
GNU tar (as root, so ownership is kept), then OCI whiteouts are applied.
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request

ACCEPT = ", ".join([
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
])


def fetch(url: str, accept: str = ACCEPT, token: str | None = None) -> tuple[bytes, str]:
    headers = {"Accept": accept}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(req, timeout=120) as r:
        return r.read(), r.headers.get("content-type", "")


def get(registry: str, repo: str, kind: str, ref: str) -> tuple[bytes, str]:
    url = f"https://{registry}/v2/{repo}/{kind}/{ref}"
    try:
        return fetch(url)
    except urllib.error.HTTPError as err:
        if err.code != 401:
            raise
        realm = err.headers.get("www-authenticate", "")
        realm_url = realm.split('realm="', 1)[1].split('"', 1)[0]
        tok, _ = fetch(f"{realm_url}?scope=repository:{repo}:pull&service={registry}", "application/json")
        token = json.loads(tok).get("token") or json.loads(tok).get("access_token")
        return fetch(url, token=token)


def verified(data: bytes, digest: str) -> bytes:
    algo, want = digest.split(":", 1)
    if algo != "sha256" or hashlib.sha256(data).hexdigest() != want:
        raise SystemExit(f"digest mismatch for {digest}")
    return data


def apply_whiteouts(root: str) -> None:
    for dirpath, _dirs, files in os.walk(root):
        for name in files:
            if not name.startswith(".wh."):
                continue
            path = os.path.join(dirpath, name)
            os.remove(path)
            if name == ".wh..wh..opq":
                continue  # opaque markers only matter across layers; handled per layer below
            target = os.path.join(dirpath, name[4:])
            if os.path.isdir(target) and not os.path.islink(target):
                shutil.rmtree(target)
            elif os.path.lexists(target):
                os.remove(target)


def main() -> int:
    registry, repo, digest, dest = sys.argv[1:5]
    manifest_raw, ctype = get(registry, repo, "manifests", digest)
    manifest = json.loads(verified(manifest_raw, digest))
    if "manifests" in manifest:  # an index: pick linux/amd64
        pick = next(m for m in manifest["manifests"]
                    if m.get("platform", {}).get("os") == "linux"
                    and m.get("platform", {}).get("architecture") == "amd64")
        manifest = json.loads(verified(get(registry, repo, "manifests", pick["digest"])[0], pick["digest"]))
    config = json.loads(verified(get(registry, repo, "blobs", manifest["config"]["digest"])[0],
                                 manifest["config"]["digest"]))
    if config.get("architecture") != "amd64" or config.get("os") != "linux":
        raise SystemExit(f"image is {config.get('os')}/{config.get('architecture')}, want linux/amd64")
    os.makedirs(dest, exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        for layer in manifest["layers"]:
            blob = verified(get(registry, repo, "blobs", layer["digest"])[0], layer["digest"])
            path = os.path.join(tmp, "layer.tar")
            with open(path, "wb") as f:
                f.write(blob)
            # Opaque directories hide everything below them from earlier layers.
            listing = subprocess.run(["tar", "-tf", path], check=True, capture_output=True, text=True).stdout
            for member in listing.splitlines():
                if member.endswith(".wh..wh..opq"):
                    opaque = os.path.join(dest, os.path.dirname(member))
                    if os.path.isdir(opaque):
                        for entry in os.listdir(opaque):
                            full = os.path.join(opaque, entry)
                            shutil.rmtree(full) if os.path.isdir(full) and not os.path.islink(full) else os.remove(full)
            subprocess.run(["tar", "-xf", path, "-C", dest, "--numeric-owner", "--same-permissions"], check=True)
            apply_whiteouts(dest)
            print(f"layer {layer['digest'][:19]} {layer['size']} bytes", flush=True)
    with open(os.path.join(dest, ".image-config.json"), "w", encoding="utf-8") as f:
        json.dump(config.get("config", {}), f)
    return 0


if __name__ == "__main__":
    sys.exit(main())
