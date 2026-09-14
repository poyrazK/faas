#!/usr/bin/env python3
"""Run rclone with a short-lived, impersonated GCS backup identity.

Non-GCS remotes pass through unchanged.  On GCE, an env-auth GCS remote uses
the VM's attached identity only to mint a short-lived token for the dedicated
append-only backup writer.  No service-account key is written to disk.
"""

from __future__ import annotations

import configparser
import json
import os
import re
import sys
import time
import urllib.request


METADATA = "http://metadata.google.internal/computeMetadata/v1"


def rclone_config(args: list[str]) -> str:
    for index, arg in enumerate(args):
        if arg.startswith("--config="):
            return arg.split("=", 1)[1]
        if arg == "--config" and index + 1 < len(args):
            return args[index + 1]
    return os.environ.get("RCLONE_CONFIG", os.path.expanduser("~/.config/rclone/rclone.conf"))


def env_auth_gcs_remote(path: str) -> str | None:
    parser = configparser.ConfigParser(interpolation=None)
    try:
        with open(path, encoding="utf-8") as stream:
            parser.read_file(stream)
    except (OSError, configparser.Error):
        return None
    for section in parser.sections():
        if parser.get(section, "type", fallback="").strip().lower() != "google cloud storage":
            continue
        if parser.getboolean(section, "env_auth", fallback=False):
            return section
    return None


def request_json(url: str, *, token: str | None = None, body: dict[str, object] | None = None) -> dict[str, object]:
    headers = {"Metadata-Flavor": "Google"} if token is None else {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json",
    }
    data = None if body is None else json.dumps(body).encode()
    last_error: Exception | None = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, data=data, headers=headers), timeout=10) as response:
                value = json.load(response)
            if not isinstance(value, dict):
                raise RuntimeError("credential endpoint returned a non-object response")
            return value
        except Exception as exc:  # network and HTTP errors share the bounded retry
            last_error = exc
            if attempt < 2:
                time.sleep(0.25 * (attempt + 1))
    raise RuntimeError(f"credential endpoint failed: {last_error}")


def main() -> int:
    args = sys.argv[1:]
    remote = env_auth_gcs_remote(rclone_config(args))
    if remote is None:
        os.execvp("rclone", ["rclone", *args])

    # The project-id metadata endpoint is plain text, unlike token endpoints.
    request = urllib.request.Request(f"{METADATA}/project/project-id", headers={"Metadata-Flavor": "Google"})
    with urllib.request.urlopen(request, timeout=10) as response:
        project = response.read().decode().strip()
    if not project:
        raise RuntimeError("metadata server returned no project id")
    backup_sa = os.environ.get("GCP_BACKUP_SERVICE_ACCOUNT", f"gregale-backup@{project}.iam.gserviceaccount.com")
    if not re.fullmatch(r"[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,61}[a-z0-9]\.iam\.gserviceaccount\.com", str(backup_sa)):
        raise RuntimeError("GCP_BACKUP_SERVICE_ACCOUNT is invalid")

    caller = request_json(f"{METADATA}/instance/service-accounts/default/token")
    caller_token = str(caller.get("access_token", ""))
    if not caller_token:
        raise RuntimeError("metadata server returned no access token")
    minted = request_json(
        f"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/{backup_sa}:generateAccessToken",
        token=caller_token,
        body={"scope": ["https://www.googleapis.com/auth/devstorage.read_write"], "lifetime": "3600s"},
    )
    access_token = str(minted.get("accessToken", ""))
    if not access_token:
        raise RuntimeError("IAM Credentials returned no impersonated access token")

    key = re.sub(r"[^A-Za-z0-9]", "_", remote).upper()
    os.environ[f"RCLONE_CONFIG_{key}_TOKEN"] = json.dumps({
        "access_token": access_token,
        "token_type": "Bearer",
        "expiry": minted.get("expireTime", ""),
    }, separators=(",", ":"))
    os.environ[f"RCLONE_CONFIG_{key}_ENV_AUTH"] = "false"
    os.execvp("rclone", ["rclone", *args])
    return 127


if __name__ == "__main__":
    raise SystemExit(main())
