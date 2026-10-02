#!/usr/bin/env python3
"""Bind regional workflow generation to the released Action's immutable pin."""

import argparse
import json
import re
import sys
import urllib.error
import urllib.request


def resolve_support(sha, opener=urllib.request.urlopen):
    if re.fullmatch(r"[0-9a-fA-F]{40}", sha) is None:
        raise ValueError("expected a full Action commit SHA")
    sha = sha.lower()
    root = f"https://raw.githubusercontent.com/poyrazK/faas/{sha}/.github/actions/deploy"
    try:
        with opener(f"{root}/src/capabilities.json", timeout=10) as response:
            data = response.read(4097)
    except urllib.error.HTTPError as error:
        try:
            if error.code == 404:
                return ""  # Older released Actions have no regional contract.
            raise
        finally:
            error.close()
    if len(data) > 4096:
        raise ValueError("Action capability manifest exceeds 4096 bytes")
    capabilities = json.loads(data)
    if not isinstance(capabilities, dict) or type(capabilities.get("schema_version")) is not int:
        raise ValueError("invalid Action capability manifest")
    if capabilities["schema_version"] != 1:
        raise ValueError("unsupported Action capability schema")
    if capabilities.get("compute_region") is not True:
        return ""
    request = urllib.request.Request(f"{root}/bin/gregale", method="HEAD")
    with opener(request, timeout=10) as response:
        if response.status != 200 or int(response.headers.get("Content-Length", "0")) <= 0:
            raise ValueError("regional Action pin has no released CLI bundle")
    return sha


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("sha")
    args = parser.parse_args()
    try:
        print(resolve_support(args.sha))
    except (ValueError, OSError, urllib.error.URLError) as error:
        print(f"could not verify regional Action capability: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
