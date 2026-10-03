from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.refresh_automatic_route_check_response_202_status import (
    RefreshAutomaticRouteCheckResponse202Status,
    check_refresh_automatic_route_check_response_202_status,
)

T = TypeVar("T", bound="RefreshAutomaticRouteCheckResponse202")


@_attrs_define
class RefreshAutomaticRouteCheckResponse202:
    app_id: UUID
    deployment_id: UUID
    status: RefreshAutomaticRouteCheckResponse202Status
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        status = check_refresh_automatic_route_check_response_202_status(d.pop("status"))

        refresh_automatic_route_check_response_202 = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            status=status,
        )

        refresh_automatic_route_check_response_202.additional_properties = d
        return refresh_automatic_route_check_response_202

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
