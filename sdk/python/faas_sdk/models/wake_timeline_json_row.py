from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.wake_timeline_json_row_kind import WakeTimelineJSONRowKind, check_wake_timeline_json_row_kind
from ..models.wake_timeline_json_row_tier import WakeTimelineJSONRowTier, check_wake_timeline_json_row_tier
from ..models.wake_timeline_json_row_trigger_class import (
    WakeTimelineJSONRowTriggerClass,
    check_wake_timeline_json_row_trigger_class,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WakeTimelineJSONRow")


@_attrs_define
class WakeTimelineJSONRow:
    """One row of `AppWakeTimelineResponse.rows`. Mirrors
    pkg/dashboard/views.WakeTimelineRow's fields so the JSON
    mirror can render the same dashboard page 1:1. The
    nullable fields (Trigger / QueuedCount / ConcurrencyAtAdmit
    / ReadyInMS) use omitempty so the dashboard SPA can
    distinguish "absent" (jsonb key missing — pre-PR-A fleet
    row) from "explicit zero" (jsonb key present and 0).
    `ready_in_ms = -1` is the em-dash sentinel for "no
    boot_completed row yet" (still booting or rejected) — the
    dashboard SPA renders "—" on -1, mirroring the HTML page
    cell-empty branch.

    """

    kind: WakeTimelineJSONRowKind
    """Event kind. Today always wake.boot_started; the field is open for future wake.boot_completed/_failed rows."""
    state: str
    """Mirror of the instance.state column on the per-app-detail recent-wakes table."""
    at_capacity: bool
    """True when admitted at the plan's per-app MaxConcurrency ceiling. Only meaningful when at_capacity_present is
    true."""
    at_capacity_present: bool
    """True when the at_capacity key was in jsonb; false = absent (pre-PR-A fleet). The dashboard renders em-dash
    when false."""
    at: datetime.datetime | Unset = UNSET
    """RFC3339 UTC timestamp of the wake."""
    wake_id: str | Unset = UNSET
    """Wake-attempt correlation ID."""
    trigger: str | Unset = UNSET
    """Closed-enum trigger that admitted the wake (manual.cron / manual.api / scheduled.idle / …). Empty/absent on
    pre-PR-A fleet rows."""
    trigger_class: WakeTimelineJSONRowTriggerClass | Unset = UNSET
    """Bounded User-Agent classification for the request that caused the wake. Empty/absent on pre-M3 fleet rows."""
    method: str | Unset = UNSET
    """Wake method (restore or cold_boot), when telemetry is available."""
    tier: WakeTimelineJSONRowTier | Unset = UNSET
    """Snapshot tier selected for the wake."""
    queued_count: int | Unset = UNSET
    """ledger.Concurrency at admit. 0 when absent."""
    concurrency_at_admit: int | Unset = UNSET
    """Same reading; 0 is the cold-start case."""
    ready_in_ms: int | Unset = UNSET
    """Wall-clock boot_started → boot_completed delta in ms. -1 when still booting or rejected. 0 is impossible (a
    0ms wake would round to a positive integer)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        state = self.state

        at_capacity = self.at_capacity

        at_capacity_present = self.at_capacity_present

        at: str | Unset = UNSET
        if not isinstance(self.at, Unset):
            at = self.at.isoformat()

        wake_id = self.wake_id

        trigger = self.trigger

        trigger_class: str | Unset = UNSET
        if not isinstance(self.trigger_class, Unset):
            trigger_class = self.trigger_class

        method = self.method

        tier: str | Unset = UNSET
        if not isinstance(self.tier, Unset):
            tier = self.tier

        queued_count = self.queued_count

        concurrency_at_admit = self.concurrency_at_admit

        ready_in_ms = self.ready_in_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "state": state,
                "at_capacity": at_capacity,
                "at_capacity_present": at_capacity_present,
            }
        )
        if at is not UNSET:
            field_dict["at"] = at
        if wake_id is not UNSET:
            field_dict["wake_id"] = wake_id
        if trigger is not UNSET:
            field_dict["trigger"] = trigger
        if trigger_class is not UNSET:
            field_dict["trigger_class"] = trigger_class
        if method is not UNSET:
            field_dict["method"] = method
        if tier is not UNSET:
            field_dict["tier"] = tier
        if queued_count is not UNSET:
            field_dict["queued_count"] = queued_count
        if concurrency_at_admit is not UNSET:
            field_dict["concurrency_at_admit"] = concurrency_at_admit
        if ready_in_ms is not UNSET:
            field_dict["ready_in_ms"] = ready_in_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_wake_timeline_json_row_kind(d.pop("kind"))

        state = d.pop("state")

        at_capacity = d.pop("at_capacity")

        at_capacity_present = d.pop("at_capacity_present")

        _at = d.pop("at", UNSET)
        at: datetime.datetime | Unset
        if isinstance(_at, Unset):
            at = UNSET
        else:
            at = datetime.datetime.fromisoformat(_at)

        wake_id = d.pop("wake_id", UNSET)

        trigger = d.pop("trigger", UNSET)

        _trigger_class = d.pop("trigger_class", UNSET)
        trigger_class: WakeTimelineJSONRowTriggerClass | Unset
        if isinstance(_trigger_class, Unset):
            trigger_class = UNSET
        else:
            trigger_class = check_wake_timeline_json_row_trigger_class(_trigger_class)

        method = d.pop("method", UNSET)

        _tier = d.pop("tier", UNSET)
        tier: WakeTimelineJSONRowTier | Unset
        if isinstance(_tier, Unset):
            tier = UNSET
        else:
            tier = check_wake_timeline_json_row_tier(_tier)

        queued_count = d.pop("queued_count", UNSET)

        concurrency_at_admit = d.pop("concurrency_at_admit", UNSET)

        ready_in_ms = d.pop("ready_in_ms", UNSET)

        wake_timeline_json_row = cls(
            kind=kind,
            state=state,
            at_capacity=at_capacity,
            at_capacity_present=at_capacity_present,
            at=at,
            wake_id=wake_id,
            trigger=trigger,
            trigger_class=trigger_class,
            method=method,
            tier=tier,
            queued_count=queued_count,
            concurrency_at_admit=concurrency_at_admit,
            ready_in_ms=ready_in_ms,
        )

        wake_timeline_json_row.additional_properties = d
        return wake_timeline_json_row

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
