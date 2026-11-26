from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="APISandboxVerification")


@_attrs_define
class APISandboxVerification:
    deployment_id: UUID
    checked_at: datetime.datetime
    openapi_status: int
    smoke_status: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        checked_at = self.checked_at.isoformat()

        openapi_status = self.openapi_status

        smoke_status = self.smoke_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "checked_at": checked_at,
                "openapi_status": openapi_status,
            }
        )
        if smoke_status is not UNSET:
            field_dict["smoke_status"] = smoke_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        openapi_status = d.pop("openapi_status")

        smoke_status = d.pop("smoke_status", UNSET)

        api_sandbox_verification = cls(
            deployment_id=deployment_id,
            checked_at=checked_at,
            openapi_status=openapi_status,
            smoke_status=smoke_status,
        )

        api_sandbox_verification.additional_properties = d
        return api_sandbox_verification

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
