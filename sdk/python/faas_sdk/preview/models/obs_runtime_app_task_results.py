from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_app_task_results_reason import (
    ObsRuntimeAppTaskResultsReason,
    check_obs_runtime_app_task_results_reason,
)
from ..models.obs_runtime_app_task_results_scope import (
    ObsRuntimeAppTaskResultsScope,
    check_obs_runtime_app_task_results_scope,
)
from ..models.obs_runtime_app_task_results_state import (
    ObsRuntimeAppTaskResultsState,
    check_obs_runtime_app_task_results_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_app_task_result_counts import ObsRuntimeAppTaskResultCounts
    from ..models.obs_runtime_app_task_result_item import ObsRuntimeAppTaskResultItem
    from ..models.obs_runtime_app_task_result_outcome import ObsRuntimeAppTaskResultOutcome


T = TypeVar("T", bound="ObsRuntimeAppTaskResults")


@_attrs_define
class ObsRuntimeAppTaskResults:
    """Advisory sampled completion acknowledgements and recovery sweep changes for v2 scheduler processes. Completion uses
    the returned durable status by release, manual or cron kind; queued means a policy retry. Recovery reports only
    requeued_restores, failed_runs and cancelled from successful sweeps. Failed running tasks can have uncertain command
    outcomes. These are process-local counter observations, not unique tasks, delivery acknowledgements, infrastructure
    fault rates or deployment gates. Baselines, resets, changed processes, stale or missing samples establish no delta.
    Comparisons require unchanged sampled process proof, matching original scrape clocks and uptime advance within the
    existing one-second tolerance. Sampled continuity cannot rule out every intervening restart.

    """

    scope: ObsRuntimeAppTaskResultsScope
    state: ObsRuntimeAppTaskResultsState
    counts: ObsRuntimeAppTaskResultCounts
    """Full-set signal coverage before clipping. Each scheduler contributes 15 completion kind/status signals and
    three recovery signals. Increased, unchanged, baseline, missing, stale, conflicting and unavailable partition
    inspected signals. Observed counts usable current points, including unknown comparisons. These counts describe
    signals, not cumulative counters, delta magnitudes or unique tasks."""
    outcomes: list[ObsRuntimeAppTaskResultOutcome]
    incomplete: bool
    items: list[ObsRuntimeAppTaskResultItem]
    omitted_items: int
    reason: ObsRuntimeAppTaskResultsReason | Unset = UNSET
    comparison_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        state: str = self.state

        counts = self.counts.to_dict()

        outcomes = []
        for outcomes_item_data in self.outcomes:
            outcomes_item = outcomes_item_data.to_dict()
            outcomes.append(outcomes_item)

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
                "outcomes": outcomes,
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
        from ..models.obs_runtime_app_task_result_counts import ObsRuntimeAppTaskResultCounts
        from ..models.obs_runtime_app_task_result_item import ObsRuntimeAppTaskResultItem
        from ..models.obs_runtime_app_task_result_outcome import ObsRuntimeAppTaskResultOutcome

        d = dict(src_dict)
        scope = check_obs_runtime_app_task_results_scope(d.pop("scope"))

        state = check_obs_runtime_app_task_results_state(d.pop("state"))

        counts = ObsRuntimeAppTaskResultCounts.from_dict(d.pop("counts"))

        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = ObsRuntimeAppTaskResultOutcome.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeAppTaskResultItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeAppTaskResultsReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_app_task_results_reason(_reason)

        _comparison_at = d.pop("comparison_at", UNSET)
        comparison_at: datetime.datetime | Unset
        if isinstance(_comparison_at, Unset):
            comparison_at = UNSET
        else:
            comparison_at = datetime.datetime.fromisoformat(_comparison_at)

        obs_runtime_app_task_results = cls(
            scope=scope,
            state=state,
            counts=counts,
            outcomes=outcomes,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
            comparison_at=comparison_at,
        )

        obs_runtime_app_task_results.additional_properties = d
        return obs_runtime_app_task_results

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
