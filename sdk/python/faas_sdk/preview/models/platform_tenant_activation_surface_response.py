from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tenant_hostname_response import TenantHostnameResponse


T = TypeVar("T", bound="PlatformTenantActivationSurfaceResponse")


@_attrs_define
class PlatformTenantActivationSurfaceResponse:
    """Observed routing and certificate readiness for one linked customer surface."""

    id: UUID
    app_id: UUID
    name: str
    status: str
    cert_state: str
    ready: bool
    hostnames: list[TenantHostnameResponse]
    cert_not_after: datetime.datetime | Unset = UNSET
    cert_last_error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        name = self.name

        status = self.status

        cert_state = self.cert_state

        ready = self.ready

        hostnames = []
        for hostnames_item_data in self.hostnames:
            hostnames_item = hostnames_item_data.to_dict()
            hostnames.append(hostnames_item)

        cert_not_after: str | Unset = UNSET
        if not isinstance(self.cert_not_after, Unset):
            cert_not_after = self.cert_not_after.isoformat()

        cert_last_error = self.cert_last_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "name": name,
                "status": status,
                "cert_state": cert_state,
                "ready": ready,
                "hostnames": hostnames,
            }
        )
        if cert_not_after is not UNSET:
            field_dict["cert_not_after"] = cert_not_after
        if cert_last_error is not UNSET:
            field_dict["cert_last_error"] = cert_last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tenant_hostname_response import TenantHostnameResponse

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        name = d.pop("name")

        status = d.pop("status")

        cert_state = d.pop("cert_state")

        ready = d.pop("ready")

        hostnames = []
        _hostnames = d.pop("hostnames")
        for hostnames_item_data in _hostnames:
            hostnames_item = TenantHostnameResponse.from_dict(hostnames_item_data)

            hostnames.append(hostnames_item)

        _cert_not_after = d.pop("cert_not_after", UNSET)
        cert_not_after: datetime.datetime | Unset
        if isinstance(_cert_not_after, Unset):
            cert_not_after = UNSET
        else:
            cert_not_after = datetime.datetime.fromisoformat(_cert_not_after)

        cert_last_error = d.pop("cert_last_error", UNSET)

        platform_tenant_activation_surface_response = cls(
            id=id,
            app_id=app_id,
            name=name,
            status=status,
            cert_state=cert_state,
            ready=ready,
            hostnames=hostnames,
            cert_not_after=cert_not_after,
            cert_last_error=cert_last_error,
        )

        platform_tenant_activation_surface_response.additional_properties = d
        return platform_tenant_activation_surface_response

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
