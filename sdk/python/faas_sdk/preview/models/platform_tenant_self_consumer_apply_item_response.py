from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_self_consumer_apply_item_response_action import (
    PlatformTenantSelfConsumerApplyItemResponseAction,
    check_platform_tenant_self_consumer_apply_item_response_action,
)
from ..models.platform_tenant_self_consumer_apply_item_response_status import (
    PlatformTenantSelfConsumerApplyItemResponseStatus,
    check_platform_tenant_self_consumer_apply_item_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantSelfConsumerApplyItemResponse")


@_attrs_define
class PlatformTenantSelfConsumerApplyItemResponse:
    """Result for one surface; a planned create omits consumer_id until the real apply runs."""

    surface_id: UUID
    external_ref: str
    name: str
    status: PlatformTenantSelfConsumerApplyItemResponseStatus
    action: PlatformTenantSelfConsumerApplyItemResponseAction
    consumer_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        surface_id = str(self.surface_id)

        external_ref = self.external_ref

        name = self.name

        status: str = self.status

        action: str = self.action

        consumer_id: str | Unset = UNSET
        if not isinstance(self.consumer_id, Unset):
            consumer_id = str(self.consumer_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "surface_id": surface_id,
                "external_ref": external_ref,
                "name": name,
                "status": status,
                "action": action,
            }
        )
        if consumer_id is not UNSET:
            field_dict["consumer_id"] = consumer_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        surface_id = UUID(d.pop("surface_id"))

        external_ref = d.pop("external_ref")

        name = d.pop("name")

        status = check_platform_tenant_self_consumer_apply_item_response_status(d.pop("status"))

        action = check_platform_tenant_self_consumer_apply_item_response_action(d.pop("action"))

        _consumer_id = d.pop("consumer_id", UNSET)
        consumer_id: UUID | Unset
        if isinstance(_consumer_id, Unset):
            consumer_id = UNSET
        else:
            consumer_id = UUID(_consumer_id)

        platform_tenant_self_consumer_apply_item_response = cls(
            surface_id=surface_id,
            external_ref=external_ref,
            name=name,
            status=status,
            action=action,
            consumer_id=consumer_id,
        )

        platform_tenant_self_consumer_apply_item_response.additional_properties = d
        return platform_tenant_self_consumer_apply_item_response

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
