from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.public_auth_status_mode import PublicAuthStatusMode, check_public_auth_status_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="PublicAuthStatus")


@_attrs_define
class PublicAuthStatus:
    """Read-only per-app public-URL auth shape on AppResponse (issue #477 / ADR-077 + ADR-118). Mirrors the row contents
    without the plaintext credentials. The redaction posture is a load-bearing invariant — see ADR-077 §Decision 're-
    redaction invariant': neither basic_user nor basic_pass is EVER returned on the wire, even when mode='basic'. To
    rotate credentials, the customer PATCHes a fresh public_auth block.

    """

    mode: PublicAuthStatusMode
"""Active auth mode. One of 'open', 'bearer', 'basic', 'ip_allowlist', 'internal_only', 'members_only'. Matches
    apps.public_auth_mode on disk; a PATCH 'open' cleared any prior sealed blob so a stale secretbox row never
    reaches a fresh request. 'internal_only' (ADR-119) requires an Authorization: Bearer JWT with
    aud='gregale.internal' signed by a Gregale daemon's Ed25519 key. 'members_only' (ADR-120) requires a valid
    faas_sid session cookie whose principal is an active member of apps.org_id — see PublicAuthBlock.mode for the
    write-side description."""
    has_basic_creds: bool
    """True iff the row carries a non-null apps.public_auth_basic blob (i.e. mode='basic' with credentials). A
    mode='basic' row without creds would 401 every request — has_basic_creds is the operator-greppable signal that
    the seal succeeded."""
    ip_allowlist_entry_count: int | Unset = UNSET
    """ADR-118: integer count of CIDRs in apps.public_auth_ip_allowlist. Returned (not the CIDR strings themselves)
    so the dashboard can show 'app X has 3 CIDRs configured' without leaking the partner-customer ranges. Always 0
    when mode != 'ip_allowlist'."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        has_basic_creds = self.has_basic_creds

        ip_allowlist_entry_count = self.ip_allowlist_entry_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "has_basic_creds": has_basic_creds,
            }
        )
        if ip_allowlist_entry_count is not UNSET:
            field_dict["ip_allowlist_entry_count"] = ip_allowlist_entry_count

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_public_auth_status_mode(d.pop("mode"))

        has_basic_creds = d.pop("has_basic_creds")

        ip_allowlist_entry_count = d.pop("ip_allowlist_entry_count", UNSET)

        public_auth_status = cls(
            mode=mode,
            has_basic_creds=has_basic_creds,
            ip_allowlist_entry_count=ip_allowlist_entry_count,
        )

        public_auth_status.additional_properties = d
        return public_auth_status

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
