from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
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

T = TypeVar("T", bound="ApplyPlatformTenantSurfaceResponse")


@_attrs_define
class ApplyPlatformTenantSurfaceResponse:
    """Existing tenant surface and its current certificate state."""

    id: UUID
    app_id: UUID
    name: str
    status: ApplyPlatformTenantSurfaceResponseStatus
    cert_state: ApplyPlatformTenantSurfaceResponseCertState
    action: ApplyPlatformTenantSurfaceResponseAction
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        name = self.name

        status: str = self.status

        cert_state: str = self.cert_state

        action: str = self.action

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "name": name,
                "status": status,
                "cert_state": cert_state,
                "action": action,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        name = d.pop("name")

        status = check_apply_platform_tenant_surface_response_status(d.pop("status"))

        cert_state = check_apply_platform_tenant_surface_response_cert_state(d.pop("cert_state"))

        action = check_apply_platform_tenant_surface_response_action(d.pop("action"))

        apply_platform_tenant_surface_response = cls(
            id=id,
            app_id=app_id,
            name=name,
            status=status,
            cert_state=cert_state,
            action=action,
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
