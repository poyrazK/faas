from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_command_cron_timing_reason import (
    ObsRuntimeCommandCronTimingReason,
    check_obs_runtime_command_cron_timing_reason,
)
from ..models.obs_runtime_command_cron_timing_scope import (
    ObsRuntimeCommandCronTimingScope,
    check_obs_runtime_command_cron_timing_scope,
)
from ..models.obs_runtime_command_cron_timing_state import (
    ObsRuntimeCommandCronTimingState,
    check_obs_runtime_command_cron_timing_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_command_cron_timing_counts import ObsRuntimeCommandCronTimingCounts
    from ..models.obs_runtime_command_cron_timing_item import ObsRuntimeCommandCronTimingItem
    from ..models.obs_runtime_command_cron_timing_signal_summary import ObsRuntimeCommandCronTimingSignalSummary


T = TypeVar("T", bound="ObsRuntimeCommandCronTiming")


@_attrs_define
class ObsRuntimeCommandCronTiming:
    """Advisory process-local command-cron admission timing for v2 scheduler jobs. Timed observations measure returned
    occurrence CreatedAt minus nominal ScheduledFor, including catch-up history and policy dispositions. This is not
    database commit time, execution-start or continuous eligibility latency. The eleven cumulative histogram buckets and
    count require complete coherent snapshots and coherent interval distributions. The unknown-timestamp counter is
    independent. Exact differences require unchanged sampled process proof, fresh matching original scrape clocks and
    uptime advance within the existing one-second tolerance. Baselines, missing evidence, restarts and resets establish
    no timing delta. No observations does not establish zero lag. No percentile, unique task count or deployment gate is
    inferred.

    """

    scope: ObsRuntimeCommandCronTimingScope
    state: ObsRuntimeCommandCronTimingState
    counts: ObsRuntimeCommandCronTimingCounts
    """Full-set counter-signal coverage before clipping, with 13 signals per scheduler target. Increased,
    unchanged, baseline, missing, stale, conflicting and unavailable partition inspected signals. Observed counts
    usable current points, including unknown comparisons. These counts classify signals, not delay magnitudes or
    unique occurrences."""
    signals: list[ObsRuntimeCommandCronTimingSignalSummary]
    incomplete: bool
    items: list[ObsRuntimeCommandCronTimingItem]
    omitted_items: int
    reason: ObsRuntimeCommandCronTimingReason | Unset = UNSET
    comparison_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        state: str = self.state

        counts = self.counts.to_dict()

        signals = []
        for signals_item_data in self.signals:
            signals_item = signals_item_data.to_dict()
            signals.append(signals_item)

        incomplete = self.incomplete

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        omitted_items = self.omitted_items

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        comparison_at: str | Unset = UNSET
        if not isinstance(self.comparison_at, Unset):
            comparison_at = self.comparison_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "state": state,
                "counts": counts,
                "signals": signals,
                "incomplete": incomplete,
                "items": items,
                "omitted_items": omitted_items,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if comparison_at is not UNSET:
            field_dict["comparison_at"] = comparison_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_command_cron_timing_counts import ObsRuntimeCommandCronTimingCounts
        from ..models.obs_runtime_command_cron_timing_item import ObsRuntimeCommandCronTimingItem
        from ..models.obs_runtime_command_cron_timing_signal_summary import ObsRuntimeCommandCronTimingSignalSummary

        d = dict(src_dict)
        scope = check_obs_runtime_command_cron_timing_scope(d.pop("scope"))

        state = check_obs_runtime_command_cron_timing_state(d.pop("state"))

        counts = ObsRuntimeCommandCronTimingCounts.from_dict(d.pop("counts"))

        signals = []
        _signals = d.pop("signals")
        for signals_item_data in _signals:
            signals_item = ObsRuntimeCommandCronTimingSignalSummary.from_dict(signals_item_data)

            signals.append(signals_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeCommandCronTimingItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeCommandCronTimingReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_command_cron_timing_reason(_reason)

        _comparison_at = d.pop("comparison_at", UNSET)
        comparison_at: datetime.datetime | Unset
        if isinstance(_comparison_at, Unset):
            comparison_at = UNSET
        else:
            comparison_at = datetime.datetime.fromisoformat(_comparison_at)

        obs_runtime_command_cron_timing = cls(
            scope=scope,
            state=state,
            counts=counts,
            signals=signals,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
            comparison_at=comparison_at,
        )

        obs_runtime_command_cron_timing.additional_properties = d
        return obs_runtime_command_cron_timing

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
