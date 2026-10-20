from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantRequestBudgetResponse")


@_attrs_define
class PlatformTenantRequestBudgetResponse:
    """One customer's authoritative cross-app admitted-request budget and current UTC counters. Absent policy has
    configured=false and zero ceilings.

    """

    tenant_id: UUID
    configured: bool
    max_requests_per_minute: int
    max_requests_per_day: int
    minute_used: int
    day_used: int
    minute_resets_at: datetime.datetime
    day_resets_at: datetime.datetime
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        configured = self.configured

        max_requests_per_minute = self.max_requests_per_minute

        max_requests_per_day = self.max_requests_per_day

        minute_used = self.minute_used

        day_used = self.day_used

        minute_resets_at = self.minute_resets_at.isoformat()

        day_resets_at = self.day_resets_at.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "configured": configured,
                "max_requests_per_minute": max_requests_per_minute,
                "max_requests_per_day": max_requests_per_day,
                "minute_used": minute_used,
                "day_used": day_used,
                "minute_resets_at": minute_resets_at,
                "day_resets_at": day_resets_at,
            }
        )
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        configured = d.pop("configured")

        max_requests_per_minute = d.pop("max_requests_per_minute")

        max_requests_per_day = d.pop("max_requests_per_day")

        minute_used = d.pop("minute_used")

        day_used = d.pop("day_used")

        minute_resets_at = datetime.datetime.fromisoformat(d.pop("minute_resets_at"))

        day_resets_at = datetime.datetime.fromisoformat(d.pop("day_resets_at"))

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        platform_tenant_request_budget_response = cls(
            tenant_id=tenant_id,
            configured=configured,
            max_requests_per_minute=max_requests_per_minute,
            max_requests_per_day=max_requests_per_day,
            minute_used=minute_used,
            day_used=day_used,
            minute_resets_at=minute_resets_at,
            day_resets_at=day_resets_at,
            updated_at=updated_at,
        )

        platform_tenant_request_budget_response.additional_properties = d
        return platform_tenant_request_budget_response

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
