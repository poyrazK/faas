from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DevSyncHistorySummary")


@_attrs_define
class DevSyncHistorySummary:
    """Aggregate trend and regression guidance for recent developer syncs."""

    count: int
    within_slo_count: int
    p50_edit_to_live_ms: int
    p95_edit_to_live_ms: int
    slo_target_ms: int
    slowest_phase: str | Unset = UNSET
    slowest_phase_ms: int | Unset = UNSET
    guidance: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        count = self.count

        within_slo_count = self.within_slo_count

        p50_edit_to_live_ms = self.p50_edit_to_live_ms

        p95_edit_to_live_ms = self.p95_edit_to_live_ms

        slo_target_ms = self.slo_target_ms

        slowest_phase = self.slowest_phase

        slowest_phase_ms = self.slowest_phase_ms

        guidance = self.guidance

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "count": count,
                "within_slo_count": within_slo_count,
                "p50_edit_to_live_ms": p50_edit_to_live_ms,
                "p95_edit_to_live_ms": p95_edit_to_live_ms,
                "slo_target_ms": slo_target_ms,
            }
        )
        if slowest_phase is not UNSET:
            field_dict["slowest_phase"] = slowest_phase
        if slowest_phase_ms is not UNSET:
            field_dict["slowest_phase_ms"] = slowest_phase_ms
        if guidance is not UNSET:
            field_dict["guidance"] = guidance

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        count = d.pop("count")

        within_slo_count = d.pop("within_slo_count")

        p50_edit_to_live_ms = d.pop("p50_edit_to_live_ms")

        p95_edit_to_live_ms = d.pop("p95_edit_to_live_ms")

        slo_target_ms = d.pop("slo_target_ms")

        slowest_phase = d.pop("slowest_phase", UNSET)

        slowest_phase_ms = d.pop("slowest_phase_ms", UNSET)

        guidance = d.pop("guidance", UNSET)

        dev_sync_history_summary = cls(
            count=count,
            within_slo_count=within_slo_count,
            p50_edit_to_live_ms=p50_edit_to_live_ms,
            p95_edit_to_live_ms=p95_edit_to_live_ms,
            slo_target_ms=slo_target_ms,
            slowest_phase=slowest_phase,
            slowest_phase_ms=slowest_phase_ms,
            guidance=guidance,
        )

        dev_sync_history_summary.additional_properties = d
        return dev_sync_history_summary

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
