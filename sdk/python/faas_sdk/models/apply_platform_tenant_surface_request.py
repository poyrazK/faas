from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.apply_platform_tenant_surface_request_cert_kind import (
    ApplyPlatformTenantSurfaceRequestCertKind,
    check_apply_platform_tenant_surface_request_cert_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplyPlatformTenantSurfaceRequest")


@_attrs_define
class ApplyPlatformTenantSurfaceRequest:
    """App-local surface to create or reuse by account-level name, with an additive set of hostnames."""

    app_id: UUID
    name: str
    hostnames: list[str]
    cert_kind: ApplyPlatformTenantSurfaceRequestCertKind | Unset = "per_host_san"
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        name = self.name

        hostnames = self.hostnames

        cert_kind: str | Unset = UNSET
        if not isinstance(self.cert_kind, Unset):
            cert_kind = self.cert_kind

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "name": name,
                "hostnames": hostnames,
            }
        )
        if cert_kind is not UNSET:
            field_dict["cert_kind"] = cert_kind

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        name = d.pop("name")

        hostnames = cast(list[str], d.pop("hostnames"))

        _cert_kind = d.pop("cert_kind", UNSET)
        cert_kind: ApplyPlatformTenantSurfaceRequestCertKind | Unset
        if isinstance(_cert_kind, Unset):
            cert_kind = UNSET
        else:
            cert_kind = check_apply_platform_tenant_surface_request_cert_kind(_cert_kind)

        apply_platform_tenant_surface_request = cls(
            app_id=app_id,
            name=name,
            hostnames=hostnames,
            cert_kind=cert_kind,
        )

        apply_platform_tenant_surface_request.additional_properties = d
        return apply_platform_tenant_surface_request

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
