from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.capabilities_response_plan import CapabilitiesResponsePlan, check_capabilities_response_plan

if TYPE_CHECKING:
    from ..models.capability_status import CapabilityStatus


T = TypeVar("T", bound="CapabilitiesResponse")


@_attrs_define
class CapabilitiesResponse:
    """Account-scoped capability registry and resolved plan entitlements."""

    registry_version: int
    """Version of the embedded product capability catalog."""
    plan: CapabilitiesResponsePlan
    capabilities: list[CapabilityStatus]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        registry_version = self.registry_version

        plan: str = self.plan

        capabilities = []
        for capabilities_item_data in self.capabilities:
            capabilities_item = capabilities_item_data.to_dict()
            capabilities.append(capabilities_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "registry_version": registry_version,
                "plan": plan,
                "capabilities": capabilities,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.capability_status import CapabilityStatus

        d = dict(src_dict)
        registry_version = d.pop("registry_version")

        plan = check_capabilities_response_plan(d.pop("plan"))

        capabilities = []
        _capabilities = d.pop("capabilities")
        for capabilities_item_data in _capabilities:
            capabilities_item = CapabilityStatus.from_dict(capabilities_item_data)

            capabilities.append(capabilities_item)

        capabilities_response = cls(
            registry_version=registry_version,
            plan=plan,
            capabilities=capabilities,
        )

        capabilities_response.additional_properties = d
        return capabilities_response

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
