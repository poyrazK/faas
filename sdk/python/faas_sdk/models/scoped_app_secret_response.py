from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ScopedAppSecretResponse")


@_attrs_define
class ScopedAppSecretResponse:
    """Per-row shape for the nested `secrets_by_scope` response
    (ADR-092 PR-B, mirror of ADR-090 D3's env_by_scope).
    Same posture as AppSecretResponse but with an explicit
    `scope` field that carries the scope name on the wire
    so a CLI / dashboard can render "scope: staging" without
    a second lookup. Value is NEVER echoed (same posture as
    AppSecretResponse).

    """

    scope: str
    key: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    kid: str | Unset = UNSET
    """age-1... recipient string of the host identity that sealed this row (ADR-089). Empty for rows sealed before
    migration 00166. Mirrors the `kid` field on the parent `AppSecretResponse` — see that schema for the cross-
    reference."""
    value_hash: str | Unset = UNSET
    """16-hex HMAC-SHA256(plaintext) keyed by the per-host host.hmac.key (ADR-117 PR-C). Empty for pre-PR-C rows."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        key = self.key

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        kid = self.kid

        value_hash = self.value_hash

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "key": key,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if kid is not UNSET:
            field_dict["kid"] = kid
        if value_hash is not UNSET:
            field_dict["value_hash"] = value_hash

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = d.pop("scope")

        key = d.pop("key")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        kid = d.pop("kid", UNSET)

        value_hash = d.pop("value_hash", UNSET)

        scoped_app_secret_response = cls(
            scope=scope,
            key=key,
            created_at=created_at,
            updated_at=updated_at,
            kid=kid,
            value_hash=value_hash,
        )

        scoped_app_secret_response.additional_properties = d
        return scoped_app_secret_response

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
