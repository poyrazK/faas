from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ScalingSchedule")


@_attrs_define
class ScalingSchedule:
    """One recurring window that raises the warm floor (ADR-195). `cron` is a five-field expression evaluated in the
    policy's `timezone`; each fire opens a window of `duration_s` seconds during which the app's floor is at least
    `min_instances`. Cron expresses instants rather than intervals, so the duration is what makes a window — and it
    needs no special case for a window that crosses midnight.

    """

    cron: str
    """Five-field cron expression (minute hour day-of-month month day-of-week), evaluated in the policy timezone."""
    duration_s: int
    """How long the window stays open after each fire, in seconds. Between 60 and 604800 (7 days). The floor of 60
    s exists because the scheduler sweep is coarser than that, so a shorter window could close before any tick
    observed it — billing a floor that never produced an instance."""
    min_instances: int
    """Warm floor while the window is open. Must be > 0: a schedule raises the floor and cannot lower it."""

    def to_dict(self) -> dict[str, Any]:
        cron = self.cron

        duration_s = self.duration_s

        min_instances = self.min_instances

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "cron": cron,
                "duration_s": duration_s,
                "min_instances": min_instances,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cron = d.pop("cron")

        duration_s = d.pop("duration_s")

        min_instances = d.pop("min_instances")

        scaling_schedule = cls(
            cron=cron,
            duration_s=duration_s,
            min_instances=min_instances,
        )

        return scaling_schedule
