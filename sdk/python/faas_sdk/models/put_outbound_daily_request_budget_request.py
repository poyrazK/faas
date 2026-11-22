from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PutOutboundDailyRequestBudgetRequest")


@_attrs_define
class PutOutboundDailyRequestBudgetRequest:
    """Set a lower per-integration daily request limit or clear it with null."""

    daily_request_limit: int | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        daily_request_limit: int | None
        daily_request_limit = self.daily_request_limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "daily_request_limit": daily_request_limit,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_daily_request_limit(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        daily_request_limit = _parse_daily_request_limit(d.pop("daily_request_limit"))

        put_outbound_daily_request_budget_request = cls(
            daily_request_limit=daily_request_limit,
        )

        put_outbound_daily_request_budget_request.additional_properties = d
        return put_outbound_daily_request_budget_request

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
