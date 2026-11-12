from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DomainBindingTarget")


@_attrs_define
class DomainBindingTarget:
    """Resolved app and environment identity captured in a reviewed plan."""

    app: str | Unset = UNSET
    app_id: str | Unset = UNSET
    environment: str | Unset = UNSET
    environment_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        app_id = self.app_id

        environment = self.environment

        environment_id = self.environment_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if app is not UNSET:
            field_dict["app"] = app
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if environment is not UNSET:
            field_dict["environment"] = environment
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app = d.pop("app", UNSET)

        app_id = d.pop("app_id", UNSET)

        environment = d.pop("environment", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        domain_binding_target = cls(
            app=app,
            app_id=app_id,
            environment=environment,
            environment_id=environment_id,
        )

        domain_binding_target.additional_properties = d
        return domain_binding_target

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
