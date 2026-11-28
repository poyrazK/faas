from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_delayed_task_dispatch_reason import (
    ObsRuntimeDelayedTaskDispatchReason,
    check_obs_runtime_delayed_task_dispatch_reason,
)
from ..models.obs_runtime_delayed_task_dispatch_scope import (
    ObsRuntimeDelayedTaskDispatchScope,
    check_obs_runtime_delayed_task_dispatch_scope,
)
from ..models.obs_runtime_delayed_task_dispatch_state import (
    ObsRuntimeDelayedTaskDispatchState,
    check_obs_runtime_delayed_task_dispatch_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_delayed_task_dispatch_counts import ObsRuntimeDelayedTaskDispatchCounts
    from ..models.obs_runtime_delayed_task_dispatch_item import ObsRuntimeDelayedTaskDispatchItem
    from ..models.obs_runtime_delayed_task_dispatch_outcome_summary import ObsRuntimeDelayedTaskDispatchOutcomeSummary


T = TypeVar("T", bound="ObsRuntimeDelayedTaskDispatch")


@_attrs_define
class ObsRuntimeDelayedTaskDispatch:
    """Advisory settled delayed-task dispatch attempt counters for v2 scheduler jobs. The four ordered outcomes are
    success, retry, failed and dead_letter. Failed includes application errors and does not identify a platform
    incident. Retry does not establish its cause or final result, and success does not prove unique or exactly-once
    execution. Exact differences require fresh original sample clocks, unchanged sampled process proof and coherent
    uptime advance within the existing one-second tolerance. Baselines, resets, changed processes and incomplete
    evidence establish no delta. No deployment gate or failure-rate threshold is inferred.

    """

    scope: ObsRuntimeDelayedTaskDispatchScope
    state: ObsRuntimeDelayedTaskDispatchState
    counts: ObsRuntimeDelayedTaskDispatchCounts
    """Full-set signal coverage before preview clipping, with four counters per scheduler target. Increased,
    unchanged, baseline, missing, stale, conflicting and unavailable partition inspected signals. Observed counts
    usable current points, including unknown comparisons. These are signal classifications, not delta magnitudes or
    unique tasks."""
    outcomes: list[ObsRuntimeDelayedTaskDispatchOutcomeSummary]
    incomplete: bool
    items: list[ObsRuntimeDelayedTaskDispatchItem]
    omitted_items: int
    reason: ObsRuntimeDelayedTaskDispatchReason | Unset = UNSET
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
        from ..models.obs_runtime_delayed_task_dispatch_counts import ObsRuntimeDelayedTaskDispatchCounts
        from ..models.obs_runtime_delayed_task_dispatch_item import ObsRuntimeDelayedTaskDispatchItem
        from ..models.obs_runtime_delayed_task_dispatch_outcome_summary import (
            ObsRuntimeDelayedTaskDispatchOutcomeSummary,
        )

        d = dict(src_dict)
        scope = check_obs_runtime_delayed_task_dispatch_scope(d.pop("scope"))

        state = check_obs_runtime_delayed_task_dispatch_state(d.pop("state"))

        counts = ObsRuntimeDelayedTaskDispatchCounts.from_dict(d.pop("counts"))

        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = ObsRuntimeDelayedTaskDispatchOutcomeSummary.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeDelayedTaskDispatchItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeDelayedTaskDispatchReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_delayed_task_dispatch_reason(_reason)

        _comparison_at = d.pop("comparison_at", UNSET)
        comparison_at: datetime.datetime | Unset
        if isinstance(_comparison_at, Unset):
            comparison_at = UNSET
        else:
            comparison_at = datetime.datetime.fromisoformat(_comparison_at)

        obs_runtime_delayed_task_dispatch = cls(
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

        obs_runtime_delayed_task_dispatch.additional_properties = d
        return obs_runtime_delayed_task_dispatch

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
