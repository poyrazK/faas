from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_app_task_kind_name import ObsRuntimeAppTaskKindName, check_obs_runtime_app_task_kind_name
from ..models.obs_runtime_app_task_result_name import (
    ObsRuntimeAppTaskResultName,
    check_obs_runtime_app_task_result_name,
)
from ..models.obs_runtime_app_task_result_source import (
    ObsRuntimeAppTaskResultSource,
    check_obs_runtime_app_task_result_source,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeAppTaskResultOutcome")


@_attrs_define
class ObsRuntimeAppTaskResultOutcome:
    """Full-set coverage for one fixed source/kind/outcome. Ordered completion kinds are release, manual, cron, each with
    succeeded, failed, timed_out, cancelled, queued, followed by recovery requeued_restores, failed_runs, cancelled.
    Kind is required for completion and absent for recovery. Counts describe signals, not numbers of tasks or
    acknowledged calls.

    """

    source: ObsRuntimeAppTaskResultSource
    """Completion acknowledges the returned durable row, including queued policy retries. Recovery counts only
    existing sweep result fields and excludes inferred retries."""
    outcome: ObsRuntimeAppTaskResultName
    """Ordered bounded outcome. Completion uses succeeded, failed, timed_out, cancelled or queued. Recovery uses
    requeued_restores, failed_runs or cancelled. A failed acknowledgement is not independently an infrastructure
    failure; failed_runs can have uncertain command outcomes."""
    increased: int
    unchanged: int
    baseline: int
    unknown: int
    kind: ObsRuntimeAppTaskKindName | Unset = UNSET
    """Closed durable task kind, separating release commands, manual tasks and cron tasks."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        outcome: str = self.outcome

        increased = self.increased

        unchanged = self.unchanged

        baseline = self.baseline

        unknown = self.unknown

        kind: str | Unset = UNSET
        if not isinstance(self.kind, Unset):
            kind = self.kind

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "outcome": outcome,
                "increased": increased,
                "unchanged": unchanged,
                "baseline": baseline,
                "unknown": unknown,
            }
        )
        if kind is not UNSET:
            field_dict["kind"] = kind

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = check_obs_runtime_app_task_result_source(d.pop("source"))

        outcome = check_obs_runtime_app_task_result_name(d.pop("outcome"))

        increased = d.pop("increased")

        unchanged = d.pop("unchanged")

        baseline = d.pop("baseline")

        unknown = d.pop("unknown")

        _kind = d.pop("kind", UNSET)
        kind: ObsRuntimeAppTaskKindName | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_obs_runtime_app_task_kind_name(_kind)

        obs_runtime_app_task_result_outcome = cls(
            source=source,
            outcome=outcome,
            increased=increased,
            unchanged=unchanged,
            baseline=baseline,
            unknown=unknown,
            kind=kind,
        )

        obs_runtime_app_task_result_outcome.additional_properties = d
        return obs_runtime_app_task_result_outcome

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
