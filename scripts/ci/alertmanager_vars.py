#!/usr/bin/env python3
"""Stage the environment's Alertmanager receiver settings into the
control plane's Ansible vars (ADR-20261121110000).

The control plane's Ansible variables come from the CP_ANSIBLE_VARS_B64
environment secret, which an operator can write but nobody, the deploy
included, can read back. When the receivers change (a host moved and its
secret files differ) the secret has to be rewritten from a copy someone
kept. This step lets the GitHub environment variable
ALERTMANAGER_VARS_OVERRIDE set the receiver settings instead: a JSON object
whose keys replace the same keys in the vars file. Receiver settings are
file paths and addresses, never secret bytes: the bytes stay in
/etc/alertmanager/secrets on the host.

Only the keys in ALLOWED may be set. A *_file value is "" (receiver off) or
a path under /etc/alertmanager/secrets/. Unset or empty: no change.

Usage: alertmanager_vars.py VARS_FILE   (reads ALERTMANAGER_VARS_OVERRIDE)
"""

from __future__ import annotations

import json
import os
import posixpath
import re
import sys

SECRETS_DIR = "/etc/alertmanager/secrets/"

FILE_KEYS = frozenset({
    "am_smtp_password_file",
    "am_pushover_token_file",
    "am_pushover_user_key_file",
    "am_discord_webhook_url_file",
    "am_slack_webhook_url_file",
    "am_discord_warn_webhook_url_file",
    "am_slack_warn_webhook_url_file",
    "am_heartbeat_url_file",
})
STRING_KEYS = frozenset({
    "am_smtp_smarthost",
    "am_smtp_auth_username",
    "am_email_from",
    "am_email_to_page",
    "am_email_to_warn",
})
BOOL_KEYS = frozenset({"am_smtp_require_tls"})
ALLOWED = FILE_KEYS | STRING_KEYS | BOOL_KEYS


class OverrideError(ValueError):
    pass


def parse_override(raw: str) -> dict[str, object]:
    """Validate ALERTMANAGER_VARS_OVERRIDE; {} when unset or blank."""
    if not raw.strip():
        return {}
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise OverrideError(f"ALERTMANAGER_VARS_OVERRIDE is not JSON: {exc}") from exc
    if not isinstance(value, dict):
        raise OverrideError("ALERTMANAGER_VARS_OVERRIDE must be a JSON object")
    for key, val in value.items():
        if key not in ALLOWED:
            raise OverrideError(f"ALERTMANAGER_VARS_OVERRIDE: {key} is not an Alertmanager receiver setting")
        if key in BOOL_KEYS:
            if not isinstance(val, bool):
                raise OverrideError(f"ALERTMANAGER_VARS_OVERRIDE: {key} must be true or false")
            continue
        if not isinstance(val, str) or any(ord(c) < 0x20 or c == "\x7f" for c in val):
            raise OverrideError(f"ALERTMANAGER_VARS_OVERRIDE: {key} must be a one-line string")
        if key in FILE_KEYS and val != "":
            if posixpath.normpath(val) != val or not val.startswith(SECRETS_DIR) or val == SECRETS_DIR:
                raise OverrideError(
                    f"ALERTMANAGER_VARS_OVERRIDE: {key} must be \"\" or a file under {SECRETS_DIR}")
    return value


def apply(text: str, override: dict[str, object]) -> str:
    """Drop the top-level lines for the overridden keys and append the new
    values. A key whose current value spans lines is refused rather than
    half-removed."""
    if not override:
        return text
    lines = text.splitlines()
    kept: list[str] = []
    for i, line in enumerate(lines):
        m = re.match(r"^([A-Za-z_][A-Za-z0-9_]*):(.*)$", line)
        if m and m.group(1) in override:
            nxt = lines[i + 1] if i + 1 < len(lines) else ""
            rest = m.group(2).strip()
            if rest in ("", "|", ">", "|-", ">-") or nxt.startswith((" ", "\t", "-")):
                raise OverrideError(f"vars set {m.group(1)} over several lines; set it on one line")
            continue
        kept.append(line)
    for key in sorted(override):
        kept.append(f"{key}: {json.dumps(override[key])}")
    return "\n".join(kept) + "\n"


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("usage: alertmanager_vars.py VARS_FILE", file=sys.stderr)
        return 2
    try:
        override = parse_override(os.environ.get("ALERTMANAGER_VARS_OVERRIDE", ""))
        if not override:
            return 0
        with open(argv[1], encoding="utf-8") as f:
            text = f.read()
        out = apply(text, override)
    except (OverrideError, OSError) as exc:
        print(f"::error::{exc}", file=sys.stderr)
        return 1
    with open(argv[1], "w", encoding="utf-8") as f:
        f.write(out)
    print(f"alertmanager_vars: set {', '.join(sorted(override))} from ALERTMANAGER_VARS_OVERRIDE")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
