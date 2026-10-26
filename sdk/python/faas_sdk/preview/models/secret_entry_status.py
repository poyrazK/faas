from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.secret_entry_status_status import SecretEntryStatusStatus, check_secret_entry_status_status

T = TypeVar("T", bound="SecretEntryStatus")


@_attrs_define
class SecretEntryStatus:
    """Whether the owner has set the requested secret. Never carries the value."""

    status: SecretEntryStatusStatus
    app: str
    key: str
    scope: str
    expires_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        app = self.app

        key = self.key

        scope = self.scope

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "app": app,
                "key": key,
                "scope": scope,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_secret_entry_status_status(d.pop("status"))

        app = d.pop("app")

        key = d.pop("key")

        scope = d.pop("scope")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        secret_entry_status = cls(
            status=status,
            app=app,
            key=key,
            scope=scope,
            expires_at=expires_at,
        )

        secret_entry_status.additional_properties = d
        return secret_entry_status

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
