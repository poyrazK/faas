from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PreflightPlanBudget")


@_attrs_define
class PreflightPlanBudget:
    """What one plan includes, expressed as running time. Preflight does not
    estimate an app's memory use, so it reports each tier's allowance
    rather than recommending one.

    """

    plan: str
    ram_mb: int
    billed_ram_mb: int
    """Plan RAM plus the fixed per-VM overhead."""
    included_running_minutes: int
    """The included allowance as wall-clock running time at this plan's billed RAM."""
    included_gb_hours: int
    price_millicents: int
    """Monthly subscription price in millicents."""
    overage_millicents_per_gb_hour: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan = self.plan

        ram_mb = self.ram_mb

        billed_ram_mb = self.billed_ram_mb

        included_running_minutes = self.included_running_minutes

        included_gb_hours = self.included_gb_hours

        price_millicents = self.price_millicents

        overage_millicents_per_gb_hour = self.overage_millicents_per_gb_hour

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan": plan,
                "ram_mb": ram_mb,
                "billed_ram_mb": billed_ram_mb,
                "included_running_minutes": included_running_minutes,
                "included_gb_hours": included_gb_hours,
                "price_millicents": price_millicents,
            }
        )
        if overage_millicents_per_gb_hour is not UNSET:
            field_dict["overage_millicents_per_gb_hour"] = overage_millicents_per_gb_hour

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan = d.pop("plan")

        ram_mb = d.pop("ram_mb")

        billed_ram_mb = d.pop("billed_ram_mb")

        included_running_minutes = d.pop("included_running_minutes")

        included_gb_hours = d.pop("included_gb_hours")

        price_millicents = d.pop("price_millicents")

        overage_millicents_per_gb_hour = d.pop("overage_millicents_per_gb_hour", UNSET)

        preflight_plan_budget = cls(
            plan=plan,
            ram_mb=ram_mb,
            billed_ram_mb=billed_ram_mb,
            included_running_minutes=included_running_minutes,
            included_gb_hours=included_gb_hours,
            price_millicents=price_millicents,
            overage_millicents_per_gb_hour=overage_millicents_per_gb_hour,
        )

        preflight_plan_budget.additional_properties = d
        return preflight_plan_budget

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
