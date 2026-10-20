from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_self_activation_response_status import (
    PlatformTenantSelfActivationResponseStatus,
    check_platform_tenant_self_activation_response_status,
)

if TYPE_CHECKING:
    from ..models.platform_tenant_self_activation_surface_response import PlatformTenantSelfActivationSurfaceResponse


T = TypeVar("T", bound="PlatformTenantSelfActivationResponse")


@_attrs_define
class PlatformTenantSelfActivationResponse:
    """Current read-only activation snapshot for the tenant bound to the bearer; ready requires every linked surface to be
    ready.

    """

    status: PlatformTenantSelfActivationResponseStatus
    enabled: bool
    ready: bool
    surfaces: list[PlatformTenantSelfActivationSurfaceResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        enabled = self.enabled

        ready = self.ready

        surfaces = []
        for surfaces_item_data in self.surfaces:
            surfaces_item = surfaces_item_data.to_dict()
            surfaces.append(surfaces_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "enabled": enabled,
                "ready": ready,
                "surfaces": surfaces,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_self_activation_surface_response import (
            PlatformTenantSelfActivationSurfaceResponse,
        )

        d = dict(src_dict)
        status = check_platform_tenant_self_activation_response_status(d.pop("status"))

        enabled = d.pop("enabled")

        ready = d.pop("ready")

        surfaces = []
        _surfaces = d.pop("surfaces")
        for surfaces_item_data in _surfaces:
            surfaces_item = PlatformTenantSelfActivationSurfaceResponse.from_dict(surfaces_item_data)

            surfaces.append(surfaces_item)

        platform_tenant_self_activation_response = cls(
            status=status,
            enabled=enabled,
            ready=ready,
            surfaces=surfaces,
        )

        platform_tenant_self_activation_response.additional_properties = d
        return platform_tenant_self_activation_response

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
