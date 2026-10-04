from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.regional_release_grant_response_status import (
    RegionalReleaseGrantResponseStatus,
    check_regional_release_grant_response_status,
)

T = TypeVar("T", bound="RegionalReleaseGrantResponse")


@_attrs_define
class RegionalReleaseGrantResponse:
    """Immutable delegation identity and current status; contains no secret or hash."""

    grant_id: UUID
    cell_id: UUID
    regional_app_id: UUID
    global_app_id: UUID
    expires_at: datetime.datetime
    status: RegionalReleaseGrantResponseStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        grant_id = str(self.grant_id)

        cell_id = str(self.cell_id)

        regional_app_id = str(self.regional_app_id)

        global_app_id = str(self.global_app_id)

        expires_at = self.expires_at.isoformat()

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "grant_id": grant_id,
                "cell_id": cell_id,
                "regional_app_id": regional_app_id,
                "global_app_id": global_app_id,
                "expires_at": expires_at,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        grant_id = UUID(d.pop("grant_id"))

        cell_id = UUID(d.pop("cell_id"))

        regional_app_id = UUID(d.pop("regional_app_id"))

        global_app_id = UUID(d.pop("global_app_id"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        status = check_regional_release_grant_response_status(d.pop("status"))

        regional_release_grant_response = cls(
            grant_id=grant_id,
            cell_id=cell_id,
            regional_app_id=regional_app_id,
            global_app_id=global_app_id,
            expires_at=expires_at,
            status=status,
        )

        regional_release_grant_response.additional_properties = d
        return regional_release_grant_response

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
