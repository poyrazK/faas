from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_attribution_reason import ProfileAttributionReason


T = TypeVar("T", bound="ProfileAttributionQuality")


@_attrs_define
class ProfileAttributionQuality:
    """Labeled and unattributed shares of all sampled CPU in the deployment window, even when a route filter is selected.
    Not VM CPU or request instrumentation completeness. Percentages are absent when no CPU was sampled.

    """

    available: bool
    total_cpu_seconds: float
    attributed_cpu_seconds: float
    unattributed_cpu_seconds: float
    diagnostics_complete: bool
    """Host discard counters reconciled against the whole merged CPU capture. False for missing or inconsistent
    counters."""
    reasons: list[ProfileAttributionReason]
    attributed_percent: float | Unset = UNSET
    unattributed_percent: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        total_cpu_seconds = self.total_cpu_seconds

        attributed_cpu_seconds = self.attributed_cpu_seconds

        unattributed_cpu_seconds = self.unattributed_cpu_seconds

        diagnostics_complete = self.diagnostics_complete

        reasons = []
        for reasons_item_data in self.reasons:
            reasons_item = reasons_item_data.to_dict()
            reasons.append(reasons_item)

        attributed_percent = self.attributed_percent

        unattributed_percent = self.unattributed_percent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "available": available,
                "total_cpu_seconds": total_cpu_seconds,
                "attributed_cpu_seconds": attributed_cpu_seconds,
                "unattributed_cpu_seconds": unattributed_cpu_seconds,
                "diagnostics_complete": diagnostics_complete,
                "reasons": reasons,
            }
        )
        if attributed_percent is not UNSET:
            field_dict["attributed_percent"] = attributed_percent
        if unattributed_percent is not UNSET:
            field_dict["unattributed_percent"] = unattributed_percent

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_attribution_reason import ProfileAttributionReason

        d = dict(src_dict)
        available = d.pop("available")

        total_cpu_seconds = d.pop("total_cpu_seconds")

        attributed_cpu_seconds = d.pop("attributed_cpu_seconds")

        unattributed_cpu_seconds = d.pop("unattributed_cpu_seconds")

        diagnostics_complete = d.pop("diagnostics_complete")

        reasons = []
        _reasons = d.pop("reasons")
        for reasons_item_data in _reasons:
            reasons_item = ProfileAttributionReason.from_dict(reasons_item_data)

            reasons.append(reasons_item)

        attributed_percent = d.pop("attributed_percent", UNSET)

        unattributed_percent = d.pop("unattributed_percent", UNSET)

        profile_attribution_quality = cls(
            available=available,
            total_cpu_seconds=total_cpu_seconds,
            attributed_cpu_seconds=attributed_cpu_seconds,
            unattributed_cpu_seconds=unattributed_cpu_seconds,
            diagnostics_complete=diagnostics_complete,
            reasons=reasons,
            attributed_percent=attributed_percent,
            unattributed_percent=unattributed_percent,
        )

        profile_attribution_quality.additional_properties = d
        return profile_attribution_quality

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
