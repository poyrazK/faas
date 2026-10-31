from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tenant_surface_response_cert_kind import (
    TenantSurfaceResponseCertKind,
    check_tenant_surface_response_cert_kind,
)
from ..models.tenant_surface_response_cert_state import (
    TenantSurfaceResponseCertState,
    check_tenant_surface_response_cert_state,
)
from ..models.tenant_surface_response_status import TenantSurfaceResponseStatus, check_tenant_surface_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tenant_hostname_response import TenantHostnameResponse


T = TypeVar("T", bound="TenantSurfaceResponse")


@_attrs_define
class TenantSurfaceResponse:
    """A tenant surface: a multi-hostname SAN bundle attached to one app. managed_by_platform_tenant is true only when
    created by a platform-tenant bundle apply.

    """

    id: str
    account_id: str
    app_id: str
    name: str
    cert_kind: TenantSurfaceResponseCertKind
    status: TenantSurfaceResponseStatus
    cert_state: TenantSurfaceResponseCertState
    hostnames: list[TenantHostnameResponse]
    cert_not_after: datetime.datetime | Unset = UNSET
    cert_last_error: None | str | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    managed_by_platform_tenant: bool | Unset = UNSET
    """True only when a platform-tenant bundle created this surface; omitted otherwise."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        account_id = self.account_id

        app_id = self.app_id

        name = self.name

        cert_kind: str = self.cert_kind

        status: str = self.status

        cert_state: str = self.cert_state

        hostnames = []
        for hostnames_item_data in self.hostnames:
            hostnames_item = hostnames_item_data.to_dict()
            hostnames.append(hostnames_item)

        cert_not_after: str | Unset = UNSET
        if not isinstance(self.cert_not_after, Unset):
            cert_not_after = self.cert_not_after.isoformat()

        cert_last_error: None | str | Unset
        if isinstance(self.cert_last_error, Unset):
            cert_last_error = UNSET
        else:
            cert_last_error = self.cert_last_error

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        managed_by_platform_tenant = self.managed_by_platform_tenant

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "app_id": app_id,
                "name": name,
                "cert_kind": cert_kind,
                "status": status,
                "cert_state": cert_state,
                "hostnames": hostnames,
            }
        )
        if cert_not_after is not UNSET:
            field_dict["cert_not_after"] = cert_not_after
        if cert_last_error is not UNSET:
            field_dict["cert_last_error"] = cert_last_error
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if managed_by_platform_tenant is not UNSET:
            field_dict["managed_by_platform_tenant"] = managed_by_platform_tenant

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tenant_hostname_response import TenantHostnameResponse

        d = dict(src_dict)
        id = d.pop("id")

        account_id = d.pop("account_id")

        app_id = d.pop("app_id")

        name = d.pop("name")

        cert_kind = check_tenant_surface_response_cert_kind(d.pop("cert_kind"))

        status = check_tenant_surface_response_status(d.pop("status"))

        cert_state = check_tenant_surface_response_cert_state(d.pop("cert_state"))

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

        def _parse_cert_last_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        cert_last_error = _parse_cert_last_error(d.pop("cert_last_error", UNSET))

        _created_at = d.pop("created_at", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        managed_by_platform_tenant = d.pop("managed_by_platform_tenant", UNSET)

        tenant_surface_response = cls(
            id=id,
            account_id=account_id,
            app_id=app_id,
            name=name,
            cert_kind=cert_kind,
            status=status,
            cert_state=cert_state,
            hostnames=hostnames,
            cert_not_after=cert_not_after,
            cert_last_error=cert_last_error,
            created_at=created_at,
            updated_at=updated_at,
            managed_by_platform_tenant=managed_by_platform_tenant,
        )

        tenant_surface_response.additional_properties = d
        return tenant_surface_response

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
