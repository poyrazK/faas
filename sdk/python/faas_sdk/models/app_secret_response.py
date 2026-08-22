from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppSecretResponse")


@_attrs_define
class AppSecretResponse:
    """A sealed secret envelope: key name, sealed ciphertext (server can't read it), version, and timestamps. Scope is the
    env-scope the row belongs to (ADR-092 PR-B). Pre-PR-B callers see scope='default' echoed on every row.

    """

    key: str
    scope: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    kid: str | Unset = UNSET
    """age-1... recipient string of the host identity that sealed this row (ADR-089). Empty for rows sealed before
    migration 00166."""
    value_hash: str | Unset = UNSET
    """16-hex HMAC-SHA256(plaintext) keyed by the per-host host.hmac.key (ADR-117 PR-C). Empty for pre-PR-C rows.
    Same value across scopes = byte-identical plaintext."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        scope = self.scope

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        kid = self.kid

        value_hash = self.value_hash

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "scope": scope,
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
        key = d.pop("key")

        scope = d.pop("scope")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        kid = d.pop("kid", UNSET)

        value_hash = d.pop("value_hash", UNSET)

        app_secret_response = cls(
            key=key,
            scope=scope,
            created_at=created_at,
            updated_at=updated_at,
            kid=kid,
            value_hash=value_hash,
        )

        app_secret_response.additional_properties = d
        return app_secret_response

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
