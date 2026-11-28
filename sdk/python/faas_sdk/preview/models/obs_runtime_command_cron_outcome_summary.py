from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_command_cron_outcome_name import (
    ObsRuntimeCommandCronOutcomeName,
    check_obs_runtime_command_cron_outcome_name,
)
from ..models.obs_runtime_command_cron_outcome_source import (
    ObsRuntimeCommandCronOutcomeSource,
    check_obs_runtime_command_cron_outcome_source,
)

T = TypeVar("T", bound="ObsRuntimeCommandCronOutcomeSummary")


@_attrs_define
class ObsRuntimeCommandCronOutcomeSummary:
    """Coverage for one ordered source/outcome across all inspected scheduler processes. Sweep has success, error,
    canceled. Decision has disabled, not_due, account_held, plan_limit, replacement_wait, not_owned, invalid_schedule,
    lookup_error, store_unavailable, no_live_deployment, canceled, legacy_queued, legacy_skipped, legacy_stale,
    legacy_error. Admission has queued, exclusive_pending, coalesced, skipped_overlap, missed_deadline, stale,
    deployment_unavailable, rejected, error, canceled, unknown. Counts describe signals with observed changes, not
    numbers of unique occurrences or calls.

    """

    source: ObsRuntimeCommandCronOutcomeSource
    """Fixed metric family. Sweep records shared scan returns; decision records pre-admission and legacy-path
    decisions; admission records actual occurrence calls and their returned dispositions. These scopes are not
    combined as unique occurrences."""
    outcome: ObsRuntimeCommandCronOutcomeName
    """Closed producer outcome. Each source has its own ordered subset. Expected scheduling policy, rejected
    admission, cancellation, stale returns, unavailable deployments, lookup/store errors and sweep or admission
    errors remain distinct. The admission outcome unknown is an actual returned classification, separate from
    missing telemetry. A rejected return is not automatically an infrastructure fault."""
    increased: int
    unchanged: int
    baseline: int
    unknown: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        outcome: str = self.outcome

        increased = self.increased

        unchanged = self.unchanged

        baseline = self.baseline

        unknown = self.unknown

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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = check_obs_runtime_command_cron_outcome_source(d.pop("source"))

        outcome = check_obs_runtime_command_cron_outcome_name(d.pop("outcome"))

        increased = d.pop("increased")

        unchanged = d.pop("unchanged")

        baseline = d.pop("baseline")

        unknown = d.pop("unknown")

        obs_runtime_command_cron_outcome_summary = cls(
            source=source,
            outcome=outcome,
            increased=increased,
            unchanged=unchanged,
            baseline=baseline,
            unknown=unknown,
        )

        obs_runtime_command_cron_outcome_summary.additional_properties = d
        return obs_runtime_command_cron_outcome_summary

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
