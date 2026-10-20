from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_self_activation_surface_response_cert_state import (
    PlatformTenantSelfActivationSurfaceResponseCertState,
    check_platform_tenant_self_activation_surface_response_cert_state,
)
from ..models.platform_tenant_self_activation_surface_response_status import (
    PlatformTenantSelfActivationSurfaceResponseStatus,
    check_platform_tenant_self_activation_surface_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.platform_tenant_self_activation_hostname_response import PlatformTenantSelfActivationHostnameResponse
    from ..models.platform_tenant_self_deployment_response import PlatformTenantSelfDeploymentResponse


T = TypeVar("T", bound="PlatformTenantSelfActivationSurfaceResponse")


@_attrs_define
class PlatformTenantSelfActivationSurfaceResponse:
    """Redacted state for one surface linked to the caller's platform tenant."""

    id: UUID
    name: str
    status: PlatformTenantSelfActivationSurfaceResponseStatus
    cert_state: PlatformTenantSelfActivationSurfaceResponseCertState
    ready: bool
    hostnames: list[PlatformTenantSelfActivationHostnameResponse]
    cert_not_after: datetime.datetime | Unset = UNSET
    latest_deployment: PlatformTenantSelfDeploymentResponse | Unset = UNSET
    """Safe status of the latest deployment attempt for the surface's app. This is not a claim that the attempt is
    currently serving; IDs, source metadata, logs, and errors are omitted."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        status: str = self.status

        cert_state: str = self.cert_state

        ready = self.ready

        hostnames = []
        for hostnames_item_data in self.hostnames:
            hostnames_item = hostnames_item_data.to_dict()
            hostnames.append(hostnames_item)

        cert_not_after: str | Unset = UNSET
        if not isinstance(self.cert_not_after, Unset):
            cert_not_after = self.cert_not_after.isoformat()

        latest_deployment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.latest_deployment, Unset):
            latest_deployment = self.latest_deployment.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "status": status,
                "cert_state": cert_state,
                "ready": ready,
                "hostnames": hostnames,
            }
        )
        if cert_not_after is not UNSET:
            field_dict["cert_not_after"] = cert_not_after
        if latest_deployment is not UNSET:
            field_dict["latest_deployment"] = latest_deployment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_self_activation_hostname_response import (
            PlatformTenantSelfActivationHostnameResponse,
        )
        from ..models.platform_tenant_self_deployment_response import PlatformTenantSelfDeploymentResponse

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        status = check_platform_tenant_self_activation_surface_response_status(d.pop("status"))

        cert_state = check_platform_tenant_self_activation_surface_response_cert_state(d.pop("cert_state"))

        ready = d.pop("ready")

        hostnames = []
        _hostnames = d.pop("hostnames")
        for hostnames_item_data in _hostnames:
            hostnames_item = PlatformTenantSelfActivationHostnameResponse.from_dict(hostnames_item_data)

            hostnames.append(hostnames_item)

        _cert_not_after = d.pop("cert_not_after", UNSET)
        cert_not_after: datetime.datetime | Unset
        if isinstance(_cert_not_after, Unset):
            cert_not_after = UNSET
        else:
            cert_not_after = datetime.datetime.fromisoformat(_cert_not_after)

        _latest_deployment = d.pop("latest_deployment", UNSET)
        latest_deployment: PlatformTenantSelfDeploymentResponse | Unset
        if isinstance(_latest_deployment, Unset):
            latest_deployment = UNSET
        else:
            latest_deployment = PlatformTenantSelfDeploymentResponse.from_dict(_latest_deployment)

        platform_tenant_self_activation_surface_response = cls(
            id=id,
            name=name,
            status=status,
            cert_state=cert_state,
            ready=ready,
            hostnames=hostnames,
            cert_not_after=cert_not_after,
            latest_deployment=latest_deployment,
        )

        platform_tenant_self_activation_surface_response.additional_properties = d
        return platform_tenant_self_activation_surface_response

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
