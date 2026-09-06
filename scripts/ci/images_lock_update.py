#!/usr/bin/env python3
"""images_lock_update.py — operator-only resolver for B3.5 + B3.6.

Resolves the current registry digest for each `pinned` entry in
images/Dockerfile.lock and rewrites BOTH the lock and the matching
`FROM ...@sha256:...` line in each Dockerfile.

The resolver uses a pure-Python manifest fetch against the Docker Registry
HTTP API and selects the platform child when a tag points at a manifest list.
It falls back to `crane digest` (the sigstore crane binary) only when the
registry API is unavailable. Registry credentials are read from
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
import urllib.parse
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
        # PR #241 review finding #9: `host in repo` was too permissive —
        # "quay.io" matched "myquay.io.example.com". Tighten to exact
        # equality or full-segment prefix: `repo == host` (bare-host
        # form, e.g. when the lock entry omitted `/library`) or
        # `repo.startswith(host + "/")` (canonical repository form).
        for host, entry in (cfg.get("auths") or {}).items():
            if repo == host or repo.startswith(host + "/"):
                auth_b64 = entry.get("auth")
                if auth_b64:
                    return {"Authorization": "Basic " + auth_b64}
    return None


def _registry_location(repo: str) -> tuple[str, str]:
    """Return the registry API host and repository path for a lock ref."""
    if repo.startswith("docker.io/"):
        return "registry-1.docker.io", repo.removeprefix("docker.io/")
    host, separator, path = repo.partition("/")
    if not separator or not path:
        return "registry-1.docker.io", f"library/{host}"
    return host, path


def _parse_bearer_challenge(challenge: str) -> tuple[str, dict[str, str]] | None:
    """Parse a Registry v2 WWW-Authenticate Bearer challenge."""
    scheme, separator, raw_params = challenge.partition(" ")
    if not separator or scheme.lower() != "bearer":
        return None
    try:
        params = urllib.request.parse_keqv_list(
            urllib.request.parse_http_list(raw_params)
        )
    except (TypeError, ValueError):
        return None
    realm = params.pop("realm", "")
    if not realm:
        return None
    return realm, params


def _registry_origin(registry: str) -> tuple[str, str, int | None]:
    """Return the HTTPS origin for a registry host[:port]."""
    parsed = urllib.parse.urlparse(f"//{registry}")
    return "https", (parsed.hostname or "").lower(), parsed.port or 443


def _trusted_bearer_realm(realm: str, registry: str) -> bool:
    """Limit token exchanges to the challenged registry's HTTPS authority."""
    try:
        parsed = urllib.parse.urlparse(realm)
        realm_origin = _url_origin(realm)
        if (
            realm_origin[0] != "https"
            or not realm_origin[1]
            or parsed.username
            or parsed.password
        ):
            return False
        # Docker Hub serves registry traffic and bearer tokens from separate,
        # fixed first-party hosts. Other supported registries keep the token
        # endpoint on their registry host.
        registry_origin = _registry_origin(registry)
        return realm_origin == registry_origin or (
            registry_origin == ("https", "registry-1.docker.io", 443)
            and realm_origin == ("https", "auth.docker.io", 443)
        )
    except ValueError:
        return False


def _url_origin(url: str) -> tuple[str, str, int | None]:
    parsed = urllib.parse.urlparse(url)
    scheme = parsed.scheme.lower()
    port = parsed.port
    if port is None:
        port = 443 if scheme == "https" else 80 if scheme == "http" else None
    return scheme, (parsed.hostname or "").lower(), port


class _AuthSafeRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Do not carry registry credentials across redirect origins."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        if redirected is not None and _url_origin(req.full_url) != _url_origin(newurl):
            redirected.remove_header("Authorization")
        return redirected


def _open_registry_request(request: urllib.request.Request, timeout: int):
    opener = urllib.request.build_opener(_AuthSafeRedirectHandler())
    return opener.open(request, timeout=timeout)


def _registry_bearer_token(
    challenge: str,
    registry: str,
    basic_auth: dict[str, str] | None,
) -> str | None:
    """Exchange a Registry v2 Bearer challenge for an anonymous/auth token."""
    parsed = _parse_bearer_challenge(challenge)
    if parsed is None:
        return None
    realm, params = parsed
    if not _trusted_bearer_realm(realm, registry):
        return None
    query = urllib.parse.urlencode(params)
    separator = "&" if urllib.parse.urlparse(realm).query else "?"
    token_url = f"{realm}{separator}{query}" if query else realm
    request = urllib.request.Request(token_url, headers=basic_auth or {})
    try:
        with _open_registry_request(request, timeout=10) as response:
            data = json.loads(response.read())
    except (urllib.error.URLError, json.JSONDecodeError, OSError):
        return None
    token = data.get("token") or data.get("access_token")
    return token if isinstance(token, str) and token else None


def resolve_via_crane(repo: str, tag: str, platform: str | None = "linux/amd64") -> str | None:
    """Try crane, optionally selecting a platform child."""
    command = ["crane", "digest"]
    if platform:
        command.extend(["--platform", platform])
    command.append(f"{repo}:{tag}")
    try:
        out = subprocess.run(
            command,
            check=True, capture_output=True, text=True, timeout=30,
        )
    except (subprocess.CalledProcessError, FileNotFoundError, subprocess.TimeoutExpired):
        return None
    digest = out.stdout.strip()
    if digest.startswith("sha256:"):
        return digest
    return None


def _select_platform_digest(manifest: dict, platform: str) -> str | None:
    """Return the child digest matching platform from an image index."""
    manifests = manifest.get("manifests")
    if not isinstance(manifests, list):
        return None
    parts = platform.split("/")
    if len(parts) < 2:
        return None
    wanted_os, wanted_arch = parts[:2]
    wanted_variant = parts[2] if len(parts) > 2 else None
    for child in manifests:
        child_platform = child.get("platform") or {}
        if child_platform.get("os") != wanted_os:
            continue
        if child_platform.get("architecture") != wanted_arch:
            continue
        if wanted_variant is not None and child_platform.get("variant") != wanted_variant:
            continue
        digest = child.get("digest")
        if isinstance(digest, str) and digest.startswith("sha256:"):
            return digest
    return None


def resolve_via_registry_api(repo: str, tag: str, platform: str) -> str | None:
    """Pure-Python fallback against the Docker Registry v2 manifest API.

    Returns sha256:... or None.
    """
    accept = (
        "application/vnd.docker.distribution.manifest.v2+json,"
        "application/vnd.docker.distribution.manifest.list.v2+json,"
        "application/vnd.oci.image.manifest.v1+json,"
        "application/vnd.oci.image.index.v1+json"
    )
    registry, registry_repo = _registry_location(repo)
    url = f"https://{registry}/v2/{registry_repo}/manifests/{tag}"
    headers: dict[str, str] = {"Accept": accept}
    auth = _auth_header_for(registry)
    if auth:
        headers.update(auth)
    # HEAD returns the manifest-list digest even when a platform is
    # requested. GET is intentional: the child descriptor is only present in
    # the index JSON. Returning the list digest here makes imaged reject the
    # pin later because it correctly refuses manifest lists at boot.
    request = urllib.request.Request(url, headers=headers, method="GET")
    try:
        response = _open_registry_request(request, timeout=15)
    except urllib.error.HTTPError as error:
        if error.code != 401:
            error.close()
            return None
        challenge = error.headers.get("WWW-Authenticate", "")
        error.close()
        token = _registry_bearer_token(
            challenge, registry, auth
        )
        if not token:
            return None
        retry_headers = {**headers, "Authorization": f"Bearer {token}"}
        retry = urllib.request.Request(url, headers=retry_headers, method="GET")
        try:
            response = _open_registry_request(retry, timeout=15)
        except (urllib.error.URLError, OSError):
            return None
    except (urllib.error.URLError, OSError):
        return None
    with response:
        raw = response.read()
        response_digest = response.headers.get("Docker-Content-Digest")
    try:
        manifest = json.loads(raw)
    except (json.JSONDecodeError, TypeError):
        return None
    return _select_platform_digest(manifest, platform) or response_digest


def resolve_digest(repo: str, tag: str, platform: str, multi_arch: bool = False) -> str | None:
    # Multi-arch Dockerfiles (builder-base) must retain the manifest-list pin
    # so buildx can resolve the correct child for each target architecture.
    # Single-arch runtime Dockerfiles need the platform child because imaged
    # rejects a manifest-list ref at staging/boot time.
    wanted_platform = "" if multi_arch else platform
    return (
        resolve_via_registry_api(repo, tag, wanted_platform)
        or resolve_via_crane(repo, tag, None if multi_arch else platform)
    )


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
        digest = resolve_digest(repo, tag, platform, bool(entry.get("multi_arch")))
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
