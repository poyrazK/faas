from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DeploymentDependencyPin")


@_attrs_define
class DeploymentDependencyPin:
    """A dependency deployment captured at admission; it never follows a later deployment."""

    service: str
    app_id: str
    deployment_id: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        service = self.service

        app_id = self.app_id

        deployment_id = self.deployment_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "service": service,
                "app_id": app_id,
                "deployment_id": deployment_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        service = d.pop("service")

        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        deployment_dependency_pin = cls(
            service=service,
            app_id=app_id,
            deployment_id=deployment_id,
        )

        deployment_dependency_pin.additional_properties = d
        return deployment_dependency_pin

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
