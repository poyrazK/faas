from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.account_health_code import AccountHealthCode
    from ..models.account_health_request import AccountHealthRequest


T = TypeVar("T", bound="AccountHealthRequests")


@_attrs_define
class AccountHealthRequests:
    """API requests Gregale refused for the account in the recent window."""

    window_hours: int
    """Length of the window in hours."""
    total: int
    """Refused requests in the window."""
    by_code: list[AccountHealthCode]
    """Counts by problem code, most frequent first, at most 10."""
    recent: list[AccountHealthRequest]
    """Most recent refused requests, at most 20."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        window_hours = self.window_hours

        total = self.total

        by_code = []
        for by_code_item_data in self.by_code:
            by_code_item = by_code_item_data.to_dict()
            by_code.append(by_code_item)

        recent = []
        for recent_item_data in self.recent:
            recent_item = recent_item_data.to_dict()
            recent.append(recent_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window_hours": window_hours,
                "total": total,
                "by_code": by_code,
                "recent": recent,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account_health_code import AccountHealthCode
        from ..models.account_health_request import AccountHealthRequest

        d = dict(src_dict)
        window_hours = d.pop("window_hours")

        total = d.pop("total")

        by_code = []
        _by_code = d.pop("by_code")
        for by_code_item_data in _by_code:
            by_code_item = AccountHealthCode.from_dict(by_code_item_data)

            by_code.append(by_code_item)

        recent = []
        _recent = d.pop("recent")
        for recent_item_data in _recent:
            recent_item = AccountHealthRequest.from_dict(recent_item_data)

            recent.append(recent_item)

        account_health_requests = cls(
            window_hours=window_hours,
            total=total,
            by_code=by_code,
            recent=recent,
        )

        account_health_requests.additional_properties = d
        return account_health_requests

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
