from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantPlanAssignmentResponse")


@_attrs_define
class PlatformTenantPlanAssignmentResponse:
    """One plan assignment and the tenant rate card it created."""

    id: UUID
    tenant_id: UUID
    plan_id: UUID
    rate_card_id: UUID
    effective_from: datetime.datetime
    created_at: datetime.datetime
    plan_name: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        tenant_id = str(self.tenant_id)

        plan_id = str(self.plan_id)

        rate_card_id = str(self.rate_card_id)

        effective_from = self.effective_from.isoformat()

        created_at = self.created_at.isoformat()

        plan_name = self.plan_name

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "tenant_id": tenant_id,
                "plan_id": plan_id,
                "rate_card_id": rate_card_id,
                "effective_from": effective_from,
                "created_at": created_at,
            }
        )
        if plan_name is not UNSET:
            field_dict["plan_name"] = plan_name

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        tenant_id = UUID(d.pop("tenant_id"))

        plan_id = UUID(d.pop("plan_id"))

        rate_card_id = UUID(d.pop("rate_card_id"))

        effective_from = datetime.datetime.fromisoformat(d.pop("effective_from"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        plan_name = d.pop("plan_name", UNSET)

        platform_tenant_plan_assignment_response = cls(
            id=id,
            tenant_id=tenant_id,
            plan_id=plan_id,
            rate_card_id=rate_card_id,
            effective_from=effective_from,
            created_at=created_at,
            plan_name=plan_name,
        )

        platform_tenant_plan_assignment_response.additional_properties = d
        return platform_tenant_plan_assignment_response

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
