from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.durable_entity_head_delivery_status import (
    DurableEntityHeadDeliveryStatus,
    check_durable_entity_head_delivery_status,
)

T = TypeVar("T", bound="DurableEntityHeadDelivery")


@_attrs_define
class DurableEntityHeadDelivery:
    """Separate observation for the pending head only; absent when no head is pending. Unknown includes unavailable/pruned
    history, not proof of non-acceptance.

    """

    status: DurableEntityHeadDeliveryStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_durable_entity_head_delivery_status(d.pop("status"))

        durable_entity_head_delivery = cls(
            status=status,
        )

        durable_entity_head_delivery.additional_properties = d
        return durable_entity_head_delivery

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
