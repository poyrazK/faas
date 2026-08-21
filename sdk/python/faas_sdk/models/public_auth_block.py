from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.public_auth_block_mode import PublicAuthBlockMode, check_public_auth_block_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="PublicAuthBlock")


@_attrs_define
class PublicAuthBlock:
    """Per-app public-URL auth write shape (issue #477 / ADR-077 + ADR-118). Sent on PATCH /v1/apps/{slug}; apid seals the
    basic_user + basic_pass into a single APP_BASIC_AUTH secretbox blob before persistence. The plaintext is never
    echoed on read (see PublicAuthStatus). For mode='ip_allowlist' (ADR-118), ip_allowlist carries the per-app CIDR
    allowlist (Pro 16 max, Scale 64 max — Free/Hobby → 403 plan_public_auth_ip_allowlist_not_allowed).

    """

    mode: PublicAuthBlockMode
    """Auth mode (closed set). 'open' is the pre-#477 default (every request passes). 'bearer' requires
    Authorization: Bearer (Hobby+; 402 on Free). 'basic' requires HTTP Basic auth with sealed credentials (Pro+; 402
    on Free/Hobby). 'ip_allowlist' (ADR-118) restricts the app to requests originating from a client IP inside the
    per-app CIDR allowlist (Pro+; 402 on Free/Hobby). 'internal_only' (ADR-119) restricts the app to requests
    carrying an Authorization: Bearer JWT with aud='gregale.internal' signed by a Gregale daemon's Ed25519 key (per-
service public-key allowlist is operator-side; available on all plans). 'members_only' (ADR-120) restricts the
    app to requests carrying a valid faas_sid session cookie whose principal is an active member of apps.org_id
    (Hobby+; 402 on Free). Unknown values → 422 invalid_public_auth_mode."""
    basic_user: str | Unset = UNSET
    """Basic-auth username (RFC 7617 §2). Plaintext at PATCH time; sealed into apps.public_auth_basic under the
    APP_BASIC_AUTH secretbox namespace. Required when mode='basic'; ignored otherwise. Range [1, 128] bytes after
    TrimSpace."""
    basic_pass: str | Unset = UNSET
    """Basic-auth password (RFC 7617 §2). Plaintext at PATCH time; sealed alongside basic_user under the
    APP_BASIC_AUTH secretbox namespace. Required when mode='basic'; ignored otherwise. Range [1, 256] bytes."""
    ip_allowlist: list[str] | Unset = UNSET
    """ADR-118: per-app ingress CIDR allowlist. Required when mode='ip_allowlist' (must be non-empty). Each entry
    is an RFC 4632 CIDR (e.g. '10.0.0.0/8' or '2001:db8::/32'); masklen /0 is rejected at the wire and by the
    apps_public_auth_ip_allowlist_cidr trigger. v4-mapped-v6 prefixes are rejected at the handler. After
    canonicalisation, the cap is plan.PublicAuthIPAllowlistMaxEntries (Pro 16, Scale 64). On the audit row, only the
    entry count is recorded — never the CIDR strings."""

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        basic_user = self.basic_user

        basic_pass = self.basic_pass

        ip_allowlist: list[str] | Unset = UNSET
        if not isinstance(self.ip_allowlist, Unset):
            ip_allowlist = self.ip_allowlist

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "mode": mode,
            }
        )
        if basic_user is not UNSET:
            field_dict["basic_user"] = basic_user
        if basic_pass is not UNSET:
            field_dict["basic_pass"] = basic_pass
        if ip_allowlist is not UNSET:
            field_dict["ip_allowlist"] = ip_allowlist

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_public_auth_block_mode(d.pop("mode"))

        basic_user = d.pop("basic_user", UNSET)

        basic_pass = d.pop("basic_pass", UNSET)

        ip_allowlist = cast(list[str], d.pop("ip_allowlist", UNSET))

        public_auth_block = cls(
            mode=mode,
            basic_user=basic_user,
            basic_pass=basic_pass,
            ip_allowlist=ip_allowlist,
        )

        return public_auth_block
