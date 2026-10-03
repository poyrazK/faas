from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.global_app_target_response_resolution_status import (
    GlobalAppTargetResponseResolutionStatus,
    check_global_app_target_response_resolution_status,
)

T = TypeVar("T", bound="GlobalAppTargetResponse")


@_attrs_define
class GlobalAppTargetResponse:
    """Verified regional app identity with current registry resolution status. This is not readiness or health."""

    cell_id: UUID
    region: str
    cell_generation: int
    regional_account_id: UUID
    regional_app_id: UUID
    regional_app_slug: str
    verified_at: datetime.datetime
    resolution_status: GlobalAppTargetResponseResolutionStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cell_id = str(self.cell_id)

        region = self.region

        cell_generation = self.cell_generation

        regional_account_id = str(self.regional_account_id)

        regional_app_id = str(self.regional_app_id)

        regional_app_slug = self.regional_app_slug

        verified_at = self.verified_at.isoformat()

        resolution_status: str = self.resolution_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cell_id": cell_id,
                "region": region,
                "cell_generation": cell_generation,
                "regional_account_id": regional_account_id,
                "regional_app_id": regional_app_id,
                "regional_app_slug": regional_app_slug,
                "verified_at": verified_at,
                "resolution_status": resolution_status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cell_id = UUID(d.pop("cell_id"))

        region = d.pop("region")

        cell_generation = d.pop("cell_generation")

        regional_account_id = UUID(d.pop("regional_account_id"))

        regional_app_id = UUID(d.pop("regional_app_id"))

        regional_app_slug = d.pop("regional_app_slug")

        verified_at = datetime.datetime.fromisoformat(d.pop("verified_at"))

        resolution_status = check_global_app_target_response_resolution_status(d.pop("resolution_status"))

        global_app_target_response = cls(
            cell_id=cell_id,
            region=region,
            cell_generation=cell_generation,
            regional_account_id=regional_account_id,
            regional_app_id=regional_app_id,
            regional_app_slug=regional_app_slug,
            verified_at=verified_at,
            resolution_status=resolution_status,
        )

        global_app_target_response.additional_properties = d
        return global_app_target_response

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
