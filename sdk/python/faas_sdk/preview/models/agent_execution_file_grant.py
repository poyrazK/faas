from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AgentExecutionFileGrant")


@_attrs_define
class AgentExecutionFileGrant:
    """Short-lived opaque token for staging one private input in the authorized Agent call."""

    grant_token: str
    """Opaque private staging token for the authorized logical call."""
    expires_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        grant_token = self.grant_token

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "grant_token": grant_token,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        grant_token = d.pop("grant_token")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        agent_execution_file_grant = cls(
            grant_token=grant_token,
            expires_at=expires_at,
        )

        agent_execution_file_grant.additional_properties = d
        return agent_execution_file_grant

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
