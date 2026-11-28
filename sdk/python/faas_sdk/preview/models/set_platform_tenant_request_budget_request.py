from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SetPlatformTenantRequestBudgetRequest")


@_attrs_define
class SetPlatformTenantRequestBudgetRequest:
    """Both optional admission ceilings must be supplied; zero disables a dimension."""

    max_requests_per_minute: int
    max_requests_per_day: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_requests_per_minute = self.max_requests_per_minute

        max_requests_per_day = self.max_requests_per_day

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "max_requests_per_minute": max_requests_per_minute,
                "max_requests_per_day": max_requests_per_day,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_requests_per_minute = d.pop("max_requests_per_minute")

        max_requests_per_day = d.pop("max_requests_per_day")

        set_platform_tenant_request_budget_request = cls(
            max_requests_per_minute=max_requests_per_minute,
            max_requests_per_day=max_requests_per_day,
        )

        set_platform_tenant_request_budget_request.additional_properties = d
        return set_platform_tenant_request_budget_request

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
