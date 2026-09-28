from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_reconciliation_plan_change_action import (
    PlatformTenantReconciliationPlanChangeAction,
    check_platform_tenant_reconciliation_plan_change_action,
)
from ..models.platform_tenant_reconciliation_plan_change_resource_type import (
    PlatformTenantReconciliationPlanChangeResourceType,
    check_platform_tenant_reconciliation_plan_change_resource_type,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantReconciliationPlanChange")


@_attrs_define
class PlatformTenantReconciliationPlanChange:
    """One deterministic plan entry. remove_candidate is informational only; no resource is detached, revoked, or deleted
    by planning.

    """

    resource_type: PlatformTenantReconciliationPlanChangeResourceType
    action: PlatformTenantReconciliationPlanChangeAction
    id: UUID | Unset = UNSET
    """Existing resource ID; absent for a resource that would be created."""
    app_id: UUID | Unset = UNSET
    external_ref: str | Unset = UNSET
    """Present for consumer entries."""
    name: str | Unset = UNSET
    """Present for consumer and surface entries."""
    surface_id: UUID | Unset = UNSET
    """Present for hostname entries on an existing surface."""
    hostname: str | Unset = UNSET
    """Present for hostname entries."""
    managed_by_platform_tenant: bool | Unset = UNSET
    """Existing provenance flag; omitted when the plan describes a resource that does not exist yet."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        resource_type: str = self.resource_type

        action: str = self.action

        id: str | Unset = UNSET
        if not isinstance(self.id, Unset):
            id = str(self.id)

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        external_ref = self.external_ref

        name = self.name

        surface_id: str | Unset = UNSET
        if not isinstance(self.surface_id, Unset):
            surface_id = str(self.surface_id)

        hostname = self.hostname

        managed_by_platform_tenant = self.managed_by_platform_tenant

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "resource_type": resource_type,
                "action": action,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if external_ref is not UNSET:
            field_dict["external_ref"] = external_ref
        if name is not UNSET:
            field_dict["name"] = name
        if surface_id is not UNSET:
            field_dict["surface_id"] = surface_id
        if hostname is not UNSET:
            field_dict["hostname"] = hostname
        if managed_by_platform_tenant is not UNSET:
            field_dict["managed_by_platform_tenant"] = managed_by_platform_tenant

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        resource_type = check_platform_tenant_reconciliation_plan_change_resource_type(d.pop("resource_type"))

        action = check_platform_tenant_reconciliation_plan_change_action(d.pop("action"))

        _id = d.pop("id", UNSET)
        id: UUID | Unset
        if isinstance(_id, Unset):
            id = UNSET
        else:
            id = UUID(_id)

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        external_ref = d.pop("external_ref", UNSET)

        name = d.pop("name", UNSET)

        _surface_id = d.pop("surface_id", UNSET)
        surface_id: UUID | Unset
        if isinstance(_surface_id, Unset):
            surface_id = UNSET
        else:
            surface_id = UUID(_surface_id)

        hostname = d.pop("hostname", UNSET)

        managed_by_platform_tenant = d.pop("managed_by_platform_tenant", UNSET)

        platform_tenant_reconciliation_plan_change = cls(
            resource_type=resource_type,
            action=action,
            id=id,
            app_id=app_id,
            external_ref=external_ref,
            name=name,
            surface_id=surface_id,
            hostname=hostname,
            managed_by_platform_tenant=managed_by_platform_tenant,
        )

        platform_tenant_reconciliation_plan_change.additional_properties = d
        return platform_tenant_reconciliation_plan_change

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
