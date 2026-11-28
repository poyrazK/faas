from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationEffectPayload")


@_attrs_define
class OperationEffectPayload:
    """Data of the operation.effect webhook. Identity comes from the platform-owned operation."""

    operation_id: UUID
    app_id: UUID
    generation: int
    name: str
    type_: str
    data: Any
    """Handler-supplied business event data."""
    platform_tenant_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        app_id = str(self.app_id)

        generation = self.generation

        name = self.name

        type_ = self.type_

        data = self.data

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation_id": operation_id,
                "app_id": app_id,
                "generation": generation,
                "name": name,
                "type": type_,
                "data": data,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        app_id = UUID(d.pop("app_id"))

        generation = d.pop("generation")

        name = d.pop("name")

        type_ = d.pop("type")

        data = d.pop("data")

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_effect_payload = cls(
            operation_id=operation_id,
            app_id=app_id,
            generation=generation,
            name=name,
            type_=type_,
            data=data,
            platform_tenant_id=platform_tenant_id,
        )

        operation_effect_payload.additional_properties = d
        return operation_effect_payload

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
