from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PrewarmRequest")


@_attrs_define
class PrewarmRequest:
    """Schedule temporary capacity restoration ahead of a demand window."""

    count: int
    """Desired number of live instances during the window; normal plan and ledger caps still apply."""
    wake_at: datetime.datetime
    """Start of the expected demand window."""
    expires_at: datetime.datetime
    """End of the temporary intent; must be after wake_at."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        count = self.count

        wake_at = self.wake_at.isoformat()

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "count": count,
                "wake_at": wake_at,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        count = d.pop("count")

        wake_at = datetime.datetime.fromisoformat(d.pop("wake_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        prewarm_request = cls(
            count=count,
            wake_at=wake_at,
            expires_at=expires_at,
        )

        prewarm_request.additional_properties = d
        return prewarm_request

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
