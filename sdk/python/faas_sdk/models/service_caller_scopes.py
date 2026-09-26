from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.service_call_scope import ServiceCallScope


T = TypeVar("T", bound="ServiceCallerScopes")


@_attrs_define
class ServiceCallerScopes:
    """Target-owned service authorization map from logical caller app name to allowed HTTP methods and path prefixes. When
    present, callers missing from the map are denied.

    """

    additional_properties: dict[str, ServiceCallScope] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.service_call_scope import ServiceCallScope

        d = dict(src_dict)
        service_caller_scopes = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = ServiceCallScope.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        service_caller_scopes.additional_properties = additional_properties
        return service_caller_scopes

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> ServiceCallScope:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: ServiceCallScope) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
