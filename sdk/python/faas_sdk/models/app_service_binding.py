from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AppServiceBinding")


@_attrs_define
class AppServiceBinding:
    """A repository-declared dependency on another same-account app. The binding is injected for discovery; gateway
    authorization remains account-scoped until a separate bindings-only policy is enabled.

    """

    binding: str
    """Platform-owned environment key containing the internal service URL."""
    service: str
    """Stable target app name used by private service discovery."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        binding = self.binding

        service = self.service

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "binding": binding,
                "service": service,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        binding = d.pop("binding")

        service = d.pop("service")

        app_service_binding = cls(
            binding=binding,
            service=service,
        )

        app_service_binding.additional_properties = d
        return app_service_binding

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
