from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.service_reliability_policy import ServiceReliabilityPolicy


T = TypeVar("T", bound="ServiceReliabilityPolicies")


@_attrs_define
class ServiceReliabilityPolicies:
    """Map of declared target service names to caller-owned reliability policies. Only names in this app's service bindings
    may appear.

    """

    additional_properties: dict[str, ServiceReliabilityPolicy] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.service_reliability_policy import ServiceReliabilityPolicy

        d = dict(src_dict)
        service_reliability_policies = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = ServiceReliabilityPolicy.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        service_reliability_policies.additional_properties = additional_properties
        return service_reliability_policies

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> ServiceReliabilityPolicy:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: ServiceReliabilityPolicy) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
