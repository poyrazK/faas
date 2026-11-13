from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AccountHealthCode")


@_attrs_define
class AccountHealthCode:
    """How often one problem code was returned in the window."""

    code: str
    """Problem code from the response body and X-Faas-Error-Code."""
    count: int
    """Responses with this code."""
    last_at: datetime.datetime
    """Most recent response with this code."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        count = self.count

        last_at = self.last_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "count": count,
                "last_at": last_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        count = d.pop("count")

        last_at = datetime.datetime.fromisoformat(d.pop("last_at"))

        account_health_code = cls(
            code=code,
            count=count,
            last_at=last_at,
        )

        account_health_code.additional_properties = d
        return account_health_code

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
