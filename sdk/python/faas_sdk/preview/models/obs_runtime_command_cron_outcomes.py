from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_command_cron_outcomes_reason import (
    ObsRuntimeCommandCronOutcomesReason,
    check_obs_runtime_command_cron_outcomes_reason,
)
from ..models.obs_runtime_command_cron_outcomes_scope import (
    ObsRuntimeCommandCronOutcomesScope,
    check_obs_runtime_command_cron_outcomes_scope,
)
from ..models.obs_runtime_command_cron_outcomes_state import (
    ObsRuntimeCommandCronOutcomesState,
    check_obs_runtime_command_cron_outcomes_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_command_cron_outcome_counts import ObsRuntimeCommandCronOutcomeCounts
    from ..models.obs_runtime_command_cron_outcome_item import ObsRuntimeCommandCronOutcomeItem
    from ..models.obs_runtime_command_cron_outcome_summary import ObsRuntimeCommandCronOutcomeSummary


T = TypeVar("T", bound="ObsRuntimeCommandCronOutcomes")


@_attrs_define
class ObsRuntimeCommandCronOutcomes:
    """Advisory process-local cron sweep, scheduling-decision and admission-return counters for v2 scheduler jobs. The 29
    fixed source/outcome signals are independent. Sweep success covers a shared scan including inline HTTP work and does
    not prove every command was admitted. Decisions are not unique due occurrences; queued, exclusive_pending and
    coalesced returns do not prove command execution or delivery. Baselines, changed processes, resets, stale or missing
    evidence establish no delta. Exact differences require unchanged sampled process proof, matching original scrape
    clocks and uptime advance within the existing one-second tolerance. Sampled continuity cannot exclude every
    intervening restart. No scheduling policy, rate or deployment gate is inferred.

    """

    scope: ObsRuntimeCommandCronOutcomesScope
    state: ObsRuntimeCommandCronOutcomesState
    counts: ObsRuntimeCommandCronOutcomeCounts
    """Full-set counter-signal coverage before clipping, with 29 signals per scheduler target. Increased,
    unchanged, baseline, missing, stale, conflicting and unavailable partition inspected signals. Observed counts
    usable current points, including unknown comparisons. Counts measure signals, not delta magnitudes, unique
    schedules, tasks or deliveries."""
    outcomes: list[ObsRuntimeCommandCronOutcomeSummary]
    incomplete: bool
    items: list[ObsRuntimeCommandCronOutcomeItem]
    omitted_items: int
    reason: ObsRuntimeCommandCronOutcomesReason | Unset = UNSET
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
        from ..models.obs_runtime_command_cron_outcome_counts import ObsRuntimeCommandCronOutcomeCounts
        from ..models.obs_runtime_command_cron_outcome_item import ObsRuntimeCommandCronOutcomeItem
        from ..models.obs_runtime_command_cron_outcome_summary import ObsRuntimeCommandCronOutcomeSummary

        d = dict(src_dict)
        scope = check_obs_runtime_command_cron_outcomes_scope(d.pop("scope"))

        state = check_obs_runtime_command_cron_outcomes_state(d.pop("state"))

        counts = ObsRuntimeCommandCronOutcomeCounts.from_dict(d.pop("counts"))

        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = ObsRuntimeCommandCronOutcomeSummary.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeCommandCronOutcomeItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeCommandCronOutcomesReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_command_cron_outcomes_reason(_reason)

        _comparison_at = d.pop("comparison_at", UNSET)
        comparison_at: datetime.datetime | Unset
        if isinstance(_comparison_at, Unset):
            comparison_at = UNSET
        else:
            comparison_at = datetime.datetime.fromisoformat(_comparison_at)

        obs_runtime_command_cron_outcomes = cls(
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

        obs_runtime_command_cron_outcomes.additional_properties = d
        return obs_runtime_command_cron_outcomes

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
