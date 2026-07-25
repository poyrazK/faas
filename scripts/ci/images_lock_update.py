#!/usr/bin/env python3
"""images_lock_update.py — operator-only resolver for B3.5 + B3.6.

Resolves the current registry digest for each `pinned` entry in
images/Dockerfile.lock and rewrites BOTH the lock and the matching
`FROM ...@sha256:...` line in each Dockerfile.

The resolver prefers `crane digest` (the sigstore crane binary) and
falls back to a pure-Python manifest v2 fetch against the Docker
Registry HTTP API. Registry credentials are read from
`~/.docker/config.json` if present (the same path `docker login`
writes) so an operator who's already authenticated doesn't need to
re-type.

Usage:
  ./scripts/ci/images_lock_update.py [--repo-root PATH] [--dry-run]

  --dry-run prints the resolved digests without writing.

Exits non-zero on any failure. Pure stdlib + urllib.

NOTE: this script is operator-only. CI does NOT call it; the
`images-lock-check` gate fails the PR until the operator runs this
locally and pushes the updated lock + Dockerfile FROM lines.
"""
from __future__ import annotations

import argparse
import base64
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

PLACEHOLDER = "sha256:REPLACE_ME_AT_MERGE_TIME"
DOCKER_CONFIG = Path.home() / ".docker" / "config.json"


def _auth_header_for(repo: str) -> dict[str, str] | None:
    """Return {'Authorization': 'Basic ...'} for repo, or None if no creds.

    Reads ~/.docker/config.json — only the `auths` map; the modern
    `credHelpers` are NOT implemented here (we don't need them for the
    public docker.io library/ namespace which crane / public registries
    can read anonymously). For private repos, the operator must set
    DOCKER_REGISTRY_USER / DOCKER_REGISTRY_TOKEN env vars.
    """
    user = os.environ.get("DOCKER_REGISTRY_USER")
    token = os.environ.get("DOCKER_REGISTRY_TOKEN")
    if user and token:
        blob = f"{user}:{token}".encode("utf-8")
        return {"Authorization": "Basic " + base64.b64encode(blob).decode("ascii")}
    if DOCKER_CONFIG.exists():
        try:
            cfg = json.loads(DOCKER_CONFIG.read_text())
        except (OSError, json.JSONDecodeError):
            return None
        # Find the matching auth entry by host prefix.
        for host, entry in (cfg.get("auths") or {}).items():
            if repo.startswith(host) or host in repo:
                auth_b64 = entry.get("auth")
                if auth_b64:
                    return {"Authorization": "Basic " + auth_b64}
    return None


def _docker_token(repo: str) -> str | None:
    """Anonymous bearer token for docker.io from the v2 token endpoint.

    Only used for `docker.io/library/...` repos that require a token
    even for anonymous pulls. The hardcoded `service` matches what
    Docker Hub expects.
    """
    if not repo.startswith("docker.io/"):
        return None
    parts = repo.split("/", 1)
    if len(parts) != 2 or not parts[1]:
        return None
    image = parts[1]
    url = (
        f"https://auth.docker.io/token?service=registry.docker.io"
        f"&scope=repository:{image}:pull"
    )
    try:
        with urllib.request.urlopen(url, timeout=10) as r:
            data = json.loads(r.read())
    except (urllib.error.URLError, json.JSONDecodeError, OSError):
        return None
    return data.get("token")


def resolve_via_crane(repo: str, tag: str) -> str | None:
    """Try `crane digest <repo>:<tag>` first. Returns sha256:... or None."""
    try:
        out = subprocess.run(
            ["crane", "digest", f"{repo}:{tag}"],
            check=True, capture_output=True, text=True, timeout=30,
        )
    except (subprocess.CalledProcessError, FileNotFoundError, subprocess.TimeoutExpired):
        return None
    digest = out.stdout.strip()
    if digest.startswith("sha256:"):
        return digest
    return None


def resolve_via_registry_api(repo: str, tag: str, platform: str) -> str | None:
    """Pure-Python fallback against the Docker Registry v2 manifest API.

    Returns sha256:... or None.
    """
    # Translate platform "linux/amd64" to the v2 accept header.
    accept = (
        "application/vnd.docker.distribution.manifest.v2+json,"
        "application/vnd.docker.distribution.manifest.list.v2+json,"
        "application/vnd.oci.image.manifest.v1+json,"
        "application/vnd.oci.image.index.v1+json"
    )
    if "/" in platform:
        plat_os, plat_arch = platform.split("/", 1)
        accept = (
            f"{accept};os={plat_os};architecture={plat_arch}"
        )
    url = f"https://registry-1.docker.io/v2/{repo}/manifests/{tag}"
    headers: dict[str, str] = {"Accept": accept}
    auth = _auth_header_for("registry-1.docker.io")
    if auth:
        headers.update(auth)
    else:
        token = _docker_token(repo)
        if token:
            headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, headers=headers, method="HEAD")
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            digest = r.headers.get("Docker-Content-Digest")
    except (urllib.error.URLError, OSError):
        return None
    if digest and digest.startswith("sha256:"):
        return digest
    return None


def resolve_digest(repo: str, tag: str, platform: str) -> str | None:
    return resolve_via_crane(repo, tag) or resolve_via_registry_api(repo, tag, platform)


def update_lock_and_dockerfiles(repo_root: Path, dry_run: bool) -> int:
    lock_path = repo_root / "images" / "Dockerfile.lock"
    lock = json.loads(lock_path.read_text(encoding="utf-8"))
    pinned = lock.get("pinned", [])
    failures: list[str] = []
    resolved: list[tuple[int, str, str, str]] = []  # (idx, dockerfile, pinned_line, new_line)

    for i, entry in enumerate(pinned):
        repo = entry["resolved_repo"]
        tag = entry["resolved_tag"]
        platform = entry.get("platform", "linux/amd64")
        digest = resolve_digest(repo, tag, platform)
        if not digest:
            failures.append(
                f"entry[{i}] {entry.get('dockerfile', '?')}: "
                f"could not resolve {repo}:{tag} for {platform}"
            )
            continue
        if digest == entry.get("digest"):
            print(f"entry[{i}] {entry.get('dockerfile', '?')}: already up to date ({digest})")
            continue
        # Rewrite lock entry + the pinned_in_dockerfile string.
        entry["digest"] = digest
        old_pinned = entry["pinned_in_dockerfile"]
        new_pinned = re.sub(r"@sha256:[0-9a-fA-F]+|@sha256:REPLACE_ME_AT_MERGE_TIME",
                            f"@{digest}", old_pinned)
        entry["pinned_in_dockerfile"] = new_pinned
        resolved.append((i, entry["dockerfile"], old_pinned, new_pinned))
        print(f"entry[{i}] {entry.get('dockerfile', '?')}: {repo}:{tag} -> {digest}")

    if failures:
        for f in failures:
            print(f"images-lock-update: FAIL: {f}", file=sys.stderr)
        return 1

    if dry_run:
        print("images-lock-update: dry run, no files written")
        return 0

    # Update the lock.
    lock["generated_at"] = _now_iso()
    lock_path.write_text(json.dumps(lock, indent=2) + "\n", encoding="utf-8")

    # Update each Dockerfile (replace the old_pinned line with the new one).
    for _, df_rel, old_line, new_line in resolved:
        df = repo_root / df_rel
        text = df.read_text(encoding="utf-8")
        if old_line not in text:
            print(f"images-lock-update: FAIL: old line not found in {df_rel}: {old_line!r}",
                  file=sys.stderr)
            return 1
        df.write_text(text.replace(old_line, new_line), encoding="utf-8")
        print(f"images-lock-update: wrote {df_rel}")

    return 0


def _now_iso() -> str:
    import datetime
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo-root", type=Path,
                    default=Path(__file__).resolve().parent.parent.parent)
    ap.add_argument("--dry-run", action="store_true",
                    help="Print resolved digests without writing files.")
    args = ap.parse_args(argv)
    return update_lock_and_dockerfiles(args.repo_root, args.dry_run)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
