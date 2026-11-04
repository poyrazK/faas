from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugStatusMemory")


@_attrs_define
class DebugStatusMemory:
    """Present when the app's guests ran short of memory in the last day: the low point and the RAM that would have kept
    30% of the guest's memory free. Shown where Gregale has released memory warnings to the account.

    """

    episodes: int
    lowest_available_mb: int
    total_mb: int
    peak_anon_mb: int
    last_at: datetime.datetime
    current_mb: int
    """The app's ram_mb."""
    suggested_mb: int
    """RAM that would have kept 30% free, in 64 MB steps; always above current_mb."""
    plan: str
    plan_mb: int
    """The most RAM the account's plan allows."""
    fits_plan: bool
    advice: str
    """The same suggestion the app health memory check gives."""
    upgrade_plan: str | Unset = UNSET
    """The smallest plan that allows suggested_mb, when fits_plan is false; absent when no plan does."""
    upgrade_mb: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        episodes = self.episodes

        lowest_available_mb = self.lowest_available_mb

        total_mb = self.total_mb

        peak_anon_mb = self.peak_anon_mb

        last_at = self.last_at.isoformat()

        current_mb = self.current_mb

        suggested_mb = self.suggested_mb

        plan = self.plan

        plan_mb = self.plan_mb

        fits_plan = self.fits_plan

        advice = self.advice

        upgrade_plan = self.upgrade_plan

        upgrade_mb = self.upgrade_mb

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "episodes": episodes,
                "lowest_available_mb": lowest_available_mb,
                "total_mb": total_mb,
                "peak_anon_mb": peak_anon_mb,
                "last_at": last_at,
                "current_mb": current_mb,
                "suggested_mb": suggested_mb,
                "plan": plan,
                "plan_mb": plan_mb,
                "fits_plan": fits_plan,
                "advice": advice,
            }
        )
        if upgrade_plan is not UNSET:
            field_dict["upgrade_plan"] = upgrade_plan
        if upgrade_mb is not UNSET:
            field_dict["upgrade_mb"] = upgrade_mb

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        episodes = d.pop("episodes")

        lowest_available_mb = d.pop("lowest_available_mb")

        total_mb = d.pop("total_mb")

        peak_anon_mb = d.pop("peak_anon_mb")

        last_at = datetime.datetime.fromisoformat(d.pop("last_at"))

        current_mb = d.pop("current_mb")

        suggested_mb = d.pop("suggested_mb")

        plan = d.pop("plan")

        plan_mb = d.pop("plan_mb")

        fits_plan = d.pop("fits_plan")

        advice = d.pop("advice")

        upgrade_plan = d.pop("upgrade_plan", UNSET)

        upgrade_mb = d.pop("upgrade_mb", UNSET)

        debug_status_memory = cls(
            episodes=episodes,
            lowest_available_mb=lowest_available_mb,
            total_mb=total_mb,
            peak_anon_mb=peak_anon_mb,
            last_at=last_at,
            current_mb=current_mb,
            suggested_mb=suggested_mb,
            plan=plan,
            plan_mb=plan_mb,
            fits_plan=fits_plan,
            advice=advice,
            upgrade_plan=upgrade_plan,
            upgrade_mb=upgrade_mb,
        )

        debug_status_memory.additional_properties = d
        return debug_status_memory

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
