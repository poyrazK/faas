from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EnableDurableEntityRegistryBody")


@_attrs_define
class EnableDurableEntityRegistryBody:
    """Enable durable entity registry — requestBody / content / application/json / schema: Preview mutation input with
    server-side size, scope and revision validation.

    """

    operation_id: UUID
    """Enable durable entity registry — requestBody / content / application/json / schema / properties / operation
    id: Fresh operation identity; replay must retain this identity and the exact request."""
    expected_revision: str
    """Enable durable entity registry — requestBody / content / application/json / schema / properties / expected
    revision: Exact opaque revision returned by inspection; never convert it to a number."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        expected_revision = self.expected_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation_id": operation_id,
                "expected_revision": expected_revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        expected_revision = d.pop("expected_revision")

        enable_durable_entity_registry_body = cls(
            operation_id=operation_id,
            expected_revision=expected_revision,
        )

        enable_durable_entity_registry_body.additional_properties = d
        return enable_durable_entity_registry_body

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
