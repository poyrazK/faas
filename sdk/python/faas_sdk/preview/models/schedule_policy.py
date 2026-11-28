from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.schedule_policy_missed_runs import SchedulePolicyMissedRuns, check_schedule_policy_missed_runs
from ..models.schedule_policy_overlap import SchedulePolicyOverlap, check_schedule_policy_overlap
from ..models.schedule_policy_version import SchedulePolicyVersion, check_schedule_policy_version
from ..types import UNSET, Unset

T = TypeVar("T", bound="SchedulePolicy")


@_attrs_define
class SchedulePolicy:
    """Versioned recurring-work scheduling policy for Jobs and both HTTP and command Crons. HTTP replace waits for a prior
    dispatched request to complete because the scheduler has no stop acknowledgement for a request already delivered to
    the app.

    """

    version: SchedulePolicyVersion
    overlap: SchedulePolicyOverlap
    """Allow concurrent occurrences, record-and-skip while one is active, or replace when previous work can be
    stopped safely. HTTP Crons wait for a dispatched request to finish."""
    missed_runs: SchedulePolicyMissedRuns
    """On scheduler recovery, coalesce the due backlog into its latest occurrence or record stale occurrences as
    skipped."""
    start_deadline_seconds: int | Unset = UNSET
    """Maximum delay from the nominal schedule time to the first task start; zero or omitted disables the deadline."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        overlap: str = self.overlap

        missed_runs: str = self.missed_runs

        start_deadline_seconds = self.start_deadline_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "overlap": overlap,
                "missed_runs": missed_runs,
            }
        )
        if start_deadline_seconds is not UNSET:
            field_dict["start_deadline_seconds"] = start_deadline_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_schedule_policy_version(d.pop("version"))

        overlap = check_schedule_policy_overlap(d.pop("overlap"))

        missed_runs = check_schedule_policy_missed_runs(d.pop("missed_runs"))

        start_deadline_seconds = d.pop("start_deadline_seconds", UNSET)

        schedule_policy = cls(
            version=version,
            overlap=overlap,
            missed_runs=missed_runs,
            start_deadline_seconds=start_deadline_seconds,
        )

        schedule_policy.additional_properties = d
        return schedule_policy

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
