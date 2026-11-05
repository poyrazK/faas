from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ApplyPlatformTenantOffboardingRequest")


@_attrs_define
class ApplyPlatformTenantOffboardingRequest:
    """Confirmation digest returned by the preview for the exact offboarding plan being applied."""

    expected_plan_hash: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_plan_hash = self.expected_plan_hash

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_plan_hash": expected_plan_hash,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_plan_hash = d.pop("expected_plan_hash")

        apply_platform_tenant_offboarding_request = cls(
            expected_plan_hash=expected_plan_hash,
        )

        apply_platform_tenant_offboarding_request.additional_properties = d
        return apply_platform_tenant_offboarding_request

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
