from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.org_plan_response_plan import OrgPlanResponsePlan, check_org_plan_response_plan
from ..types import UNSET, Unset

T = TypeVar("T", bound="OrgPlanResponse")


@_attrs_define
class OrgPlanResponse:
    """Provider-confirmed organization entitlements and any pending change."""

    org_id: UUID
    plan: OrgPlanResponsePlan
    plan_change_status: str | Unset = UNSET
    requested_plan: str | Unset = UNSET
    effective_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        org_id = str(self.org_id)

        plan: str = self.plan

        plan_change_status = self.plan_change_status

        requested_plan = self.requested_plan

        effective_at: str | Unset = UNSET
        if not isinstance(self.effective_at, Unset):
            effective_at = self.effective_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "org_id": org_id,
                "plan": plan,
            }
        )
        if plan_change_status is not UNSET:
            field_dict["plan_change_status"] = plan_change_status
        if requested_plan is not UNSET:
            field_dict["requested_plan"] = requested_plan
        if effective_at is not UNSET:
            field_dict["effective_at"] = effective_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        org_id = UUID(d.pop("org_id"))

        plan = check_org_plan_response_plan(d.pop("plan"))

        plan_change_status = d.pop("plan_change_status", UNSET)

        requested_plan = d.pop("requested_plan", UNSET)

        _effective_at = d.pop("effective_at", UNSET)
        effective_at: datetime.datetime | Unset
        if isinstance(_effective_at, Unset):
            effective_at = UNSET
        else:
            effective_at = datetime.datetime.fromisoformat(_effective_at)

        org_plan_response = cls(
            org_id=org_id,
            plan=plan,
            plan_change_status=plan_change_status,
            requested_plan=requested_plan,
            effective_at=effective_at,
        )

        org_plan_response.additional_properties = d
        return org_plan_response

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
