from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.apply_platform_tenant_response_action import (
    ApplyPlatformTenantResponseAction,
    check_apply_platform_tenant_response_action,
)
from ..models.apply_platform_tenant_response_status import (
    ApplyPlatformTenantResponseStatus,
    check_apply_platform_tenant_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.apply_platform_tenant_consumer_response import ApplyPlatformTenantConsumerResponse
    from ..models.apply_platform_tenant_surface_response import ApplyPlatformTenantSurfaceResponse


T = TypeVar("T", bound="ApplyPlatformTenantResponse")


@_attrs_define
class ApplyPlatformTenantResponse:
    """Tenant onboarding plan or applied result; omitted IDs in a dry run would be created."""

    external_ref: str
    name: str
    status: ApplyPlatformTenantResponseStatus
    action: ApplyPlatformTenantResponseAction
    dry_run: bool
    consumers: list[ApplyPlatformTenantConsumerResponse]
    surfaces: list[ApplyPlatformTenantSurfaceResponse]
    tenant_id: UUID | Unset = UNSET
    """Absent when a dry run would create this tenant."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        external_ref = self.external_ref

        name = self.name

        status: str = self.status

        action: str = self.action

        dry_run = self.dry_run

        consumers = []
        for consumers_item_data in self.consumers:
            consumers_item = consumers_item_data.to_dict()
            consumers.append(consumers_item)

        surfaces = []
        for surfaces_item_data in self.surfaces:
            surfaces_item = surfaces_item_data.to_dict()
            surfaces.append(surfaces_item)

        tenant_id: str | Unset = UNSET
        if not isinstance(self.tenant_id, Unset):
            tenant_id = str(self.tenant_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "external_ref": external_ref,
                "name": name,
                "status": status,
                "action": action,
                "dry_run": dry_run,
                "consumers": consumers,
                "surfaces": surfaces,
            }
        )
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.apply_platform_tenant_consumer_response import ApplyPlatformTenantConsumerResponse
        from ..models.apply_platform_tenant_surface_response import ApplyPlatformTenantSurfaceResponse

        d = dict(src_dict)
        external_ref = d.pop("external_ref")

        name = d.pop("name")

        status = check_apply_platform_tenant_response_status(d.pop("status"))

        action = check_apply_platform_tenant_response_action(d.pop("action"))

        dry_run = d.pop("dry_run")

        consumers = []
        _consumers = d.pop("consumers")
        for consumers_item_data in _consumers:
            consumers_item = ApplyPlatformTenantConsumerResponse.from_dict(consumers_item_data)

            consumers.append(consumers_item)

        surfaces = []
        _surfaces = d.pop("surfaces")
        for surfaces_item_data in _surfaces:
            surfaces_item = ApplyPlatformTenantSurfaceResponse.from_dict(surfaces_item_data)

            surfaces.append(surfaces_item)

        _tenant_id = d.pop("tenant_id", UNSET)
        tenant_id: UUID | Unset
        if isinstance(_tenant_id, Unset):
            tenant_id = UNSET
        else:
            tenant_id = UUID(_tenant_id)

        apply_platform_tenant_response = cls(
            external_ref=external_ref,
            name=name,
            status=status,
            action=action,
            dry_run=dry_run,
            consumers=consumers,
            surfaces=surfaces,
            tenant_id=tenant_id,
        )

        apply_platform_tenant_response.additional_properties = d
        return apply_platform_tenant_response

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
