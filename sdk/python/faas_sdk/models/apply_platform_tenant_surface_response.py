from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.apply_platform_tenant_surface_response_action import (
    ApplyPlatformTenantSurfaceResponseAction,
    check_apply_platform_tenant_surface_response_action,
)
from ..models.apply_platform_tenant_surface_response_cert_state import (
    ApplyPlatformTenantSurfaceResponseCertState,
    check_apply_platform_tenant_surface_response_cert_state,
)
from ..models.apply_platform_tenant_surface_response_status import (
    ApplyPlatformTenantSurfaceResponseStatus,
    check_apply_platform_tenant_surface_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.apply_platform_tenant_hostname_response import ApplyPlatformTenantHostnameResponse


T = TypeVar("T", bound="ApplyPlatformTenantSurfaceResponse")


@_attrs_define
class ApplyPlatformTenantSurfaceResponse:
    """Planned or applied tenant surface, hostname challenges, and current certificate state."""

    app_id: UUID
    name: str
    status: ApplyPlatformTenantSurfaceResponseStatus
    cert_state: ApplyPlatformTenantSurfaceResponseCertState
    action: ApplyPlatformTenantSurfaceResponseAction
    id: UUID | Unset = UNSET
    """Absent when a dry run would create the surface."""
    hostnames: list[ApplyPlatformTenantHostnameResponse] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        name = self.name

        status: str = self.status

        cert_state: str = self.cert_state

        action: str = self.action

        id: str | Unset = UNSET
        if not isinstance(self.id, Unset):
            id = str(self.id)

        hostnames: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.hostnames, Unset):
            hostnames = []
            for hostnames_item_data in self.hostnames:
                hostnames_item = hostnames_item_data.to_dict()
                hostnames.append(hostnames_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "name": name,
                "status": status,
                "cert_state": cert_state,
                "action": action,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if hostnames is not UNSET:
            field_dict["hostnames"] = hostnames

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.apply_platform_tenant_hostname_response import ApplyPlatformTenantHostnameResponse

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        name = d.pop("name")

        status = check_apply_platform_tenant_surface_response_status(d.pop("status"))

        cert_state = check_apply_platform_tenant_surface_response_cert_state(d.pop("cert_state"))

        action = check_apply_platform_tenant_surface_response_action(d.pop("action"))

        _id = d.pop("id", UNSET)
        id: UUID | Unset
        if isinstance(_id, Unset):
            id = UNSET
        else:
            id = UUID(_id)

        _hostnames = d.pop("hostnames", UNSET)
        hostnames: list[ApplyPlatformTenantHostnameResponse] | Unset = UNSET
        if _hostnames is not UNSET:
            hostnames = []
            for hostnames_item_data in _hostnames:
                hostnames_item = ApplyPlatformTenantHostnameResponse.from_dict(hostnames_item_data)

                hostnames.append(hostnames_item)

        apply_platform_tenant_surface_response = cls(
            app_id=app_id,
            name=name,
            status=status,
            cert_state=cert_state,
            action=action,
            id=id,
            hostnames=hostnames,
        )

        apply_platform_tenant_surface_response.additional_properties = d
        return apply_platform_tenant_surface_response

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
