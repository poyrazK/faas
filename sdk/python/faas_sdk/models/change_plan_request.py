from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.change_plan_request_plan import ChangePlanRequestPlan, check_change_plan_request_plan
from ..types import UNSET, Unset

T = TypeVar("T", bound="ChangePlanRequest")


@_attrs_define
class ChangePlanRequest:
    """Target plan for the change."""

    plan: ChangePlanRequestPlan
    salesperson_code: str | Unset = UNSET
    """Optional salesperson code for a personal account's first paid purchase.
    Letters are normalized to uppercase and surrounding spaces are removed.
    Requires a live verified session. Only active sales codes are accepted;
    existing referrals and prior payments cannot be reassigned. The choice
    remains saved if checkout is retried or abandoned. Omit or leave empty
    to keep ordinary billing behavior. Organization purchases are excluded.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan: str = self.plan

        salesperson_code = self.salesperson_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan": plan,
            }
        )
        if salesperson_code is not UNSET:
            field_dict["salesperson_code"] = salesperson_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan = check_change_plan_request_plan(d.pop("plan"))

        salesperson_code = d.pop("salesperson_code", UNSET)

        change_plan_request = cls(
            plan=plan,
            salesperson_code=salesperson_code,
        )

        change_plan_request.additional_properties = d
        return change_plan_request

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
