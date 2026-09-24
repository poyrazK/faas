from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.apply_platform_tenant_consumer_response_action import (
    ApplyPlatformTenantConsumerResponseAction,
    check_apply_platform_tenant_consumer_response_action,
)
from ..models.apply_platform_tenant_consumer_response_status import (
    ApplyPlatformTenantConsumerResponseStatus,
    check_apply_platform_tenant_consumer_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplyPlatformTenantConsumerResponse")


@_attrs_define
class ApplyPlatformTenantConsumerResponse:
    """Planned or applied app consumer with its reconciliation action."""

    app_id: UUID
    external_ref: str
    name: str
    status: ApplyPlatformTenantConsumerResponseStatus
    action: ApplyPlatformTenantConsumerResponseAction
    id: UUID | Unset = UNSET
    """Absent when a dry run would create this consumer."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        external_ref = self.external_ref

        name = self.name

        status: str = self.status

        action: str = self.action

        id: str | Unset = UNSET
        if not isinstance(self.id, Unset):
            id = str(self.id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "external_ref": external_ref,
                "name": name,
                "status": status,
                "action": action,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        external_ref = d.pop("external_ref")

        name = d.pop("name")

        status = check_apply_platform_tenant_consumer_response_status(d.pop("status"))

        action = check_apply_platform_tenant_consumer_response_action(d.pop("action"))

        _id = d.pop("id", UNSET)
        id: UUID | Unset
        if isinstance(_id, Unset):
            id = UNSET
        else:
            id = UUID(_id)

        apply_platform_tenant_consumer_response = cls(
            app_id=app_id,
            external_ref=external_ref,
            name=name,
            status=status,
            action=action,
            id=id,
        )

        apply_platform_tenant_consumer_response.additional_properties = d
        return apply_platform_tenant_consumer_response

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
