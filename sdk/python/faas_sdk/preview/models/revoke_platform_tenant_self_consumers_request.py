from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RevokePlatformTenantSelfConsumersRequest")


@_attrs_define
class RevokePlatformTenantSelfConsumersRequest:
    """All-or-nothing batch of customer IDs from this tenant's linked-consumer inventory."""

    consumer_ids: list[UUID]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_ids = []
        for consumer_ids_item_data in self.consumer_ids:
            consumer_ids_item = str(consumer_ids_item_data)
            consumer_ids.append(consumer_ids_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumer_ids": consumer_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        consumer_ids = []
        _consumer_ids = d.pop("consumer_ids")
        for consumer_ids_item_data in _consumer_ids:
            consumer_ids_item = UUID(consumer_ids_item_data)

            consumer_ids.append(consumer_ids_item)

        revoke_platform_tenant_self_consumers_request = cls(
            consumer_ids=consumer_ids,
        )

        revoke_platform_tenant_self_consumers_request.additional_properties = d
        return revoke_platform_tenant_self_consumers_request

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
