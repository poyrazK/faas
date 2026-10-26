from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentResponseCompanionsItem")


@_attrs_define
class DeploymentResponseCompanionsItem:
    name: str
    image: str | Unset = UNSET
    preset: str | Unset = UNSET
    type_: str | Unset = UNSET
    port: int | Unset = UNSET
    ram_mb: int | Unset = UNSET
    primary_ingress: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        image = self.image

        preset = self.preset

        type_ = self.type_

        port = self.port

        ram_mb = self.ram_mb

        primary_ingress = self.primary_ingress

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
            }
        )
        if image is not UNSET:
            field_dict["image"] = image
        if preset is not UNSET:
            field_dict["preset"] = preset
        if type_ is not UNSET:
            field_dict["type"] = type_
        if port is not UNSET:
            field_dict["port"] = port
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if primary_ingress is not UNSET:
            field_dict["primary_ingress"] = primary_ingress

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        image = d.pop("image", UNSET)

        preset = d.pop("preset", UNSET)

        type_ = d.pop("type", UNSET)

        port = d.pop("port", UNSET)

        ram_mb = d.pop("ram_mb", UNSET)

        primary_ingress = d.pop("primary_ingress", UNSET)

        deployment_response_companions_item = cls(
            name=name,
            image=image,
            preset=preset,
            type_=type_,
            port=port,
            ram_mb=ram_mb,
            primary_ingress=primary_ingress,
        )

        deployment_response_companions_item.additional_properties = d
        return deployment_response_companions_item

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
