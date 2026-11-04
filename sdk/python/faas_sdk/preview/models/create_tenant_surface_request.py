from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_tenant_surface_request_cert_kind import (
    CreateTenantSurfaceRequestCertKind,
    check_create_tenant_surface_request_cert_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateTenantSurfaceRequest")


@_attrs_define
class CreateTenantSurfaceRequest:
    """Create a tenant surface with a seed set of hostnames."""

    app_id: str
    name: str
    cert_kind: CreateTenantSurfaceRequestCertKind | Unset = UNSET
    hostnames: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        name = self.name

        cert_kind: str | Unset = UNSET
        if not isinstance(self.cert_kind, Unset):
            cert_kind = self.cert_kind

        hostnames: list[str] | Unset = UNSET
        if not isinstance(self.hostnames, Unset):
            hostnames = self.hostnames

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "name": name,
            }
        )
        if cert_kind is not UNSET:
            field_dict["cert_kind"] = cert_kind
        if hostnames is not UNSET:
            field_dict["hostnames"] = hostnames

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        name = d.pop("name")

        _cert_kind = d.pop("cert_kind", UNSET)
        cert_kind: CreateTenantSurfaceRequestCertKind | Unset
        if isinstance(_cert_kind, Unset):
            cert_kind = UNSET
        else:
            cert_kind = check_create_tenant_surface_request_cert_kind(_cert_kind)

        hostnames = cast(list[str], d.pop("hostnames", UNSET))

        create_tenant_surface_request = cls(
            app_id=app_id,
            name=name,
            cert_kind=cert_kind,
            hostnames=hostnames,
        )

        create_tenant_surface_request.additional_properties = d
        return create_tenant_surface_request

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
