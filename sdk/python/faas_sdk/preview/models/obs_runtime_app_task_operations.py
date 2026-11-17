from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_app_task_operations_reason import (
    ObsRuntimeAppTaskOperationsReason,
    check_obs_runtime_app_task_operations_reason,
)
from ..models.obs_runtime_app_task_operations_scope import (
    ObsRuntimeAppTaskOperationsScope,
    check_obs_runtime_app_task_operations_scope,
)
from ..models.obs_runtime_app_task_operations_state import (
    ObsRuntimeAppTaskOperationsState,
    check_obs_runtime_app_task_operations_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_app_task_operation_counts import ObsRuntimeAppTaskOperationCounts
    from ..models.obs_runtime_app_task_operation_item import ObsRuntimeAppTaskOperationItem
    from ..models.obs_runtime_app_task_operation_summary import ObsRuntimeAppTaskOperationSummary


T = TypeVar("T", bound="ObsRuntimeAppTaskOperations")


@_attrs_define
class ObsRuntimeAppTaskOperations:
    """Advisory observed operation counter changes for v2 scheduler jobs. Counters belong to individual processes. Five
    operations and seven outcomes are inspected without treating missing capacity series as zero. First samples are
    baselines, not newly observed failures. Deltas require unchanged sampled process identity, fresh points at both
    pinned times and matching original scrape clocks. Uptime advance must match elapsed scrape time within the existing
    one-second reset tolerance. Sampled continuity cannot rule out every intervening restart. These are observed sample
    differences, not rates, exact event times or command verdicts.

    """

    scope: ObsRuntimeAppTaskOperationsScope
    state: ObsRuntimeAppTaskOperationsState
    counts: ObsRuntimeAppTaskOperationCounts
    """Signal coverage over all inspected scheduler targets before preview clipping. Each target contributes 35
    operation/outcome signals. Increased, unchanged, baseline, missing, stale, conflicting and unavailable partition
    inspected signals. Observed counts usable current points, including unknown comparisons. Counts describe
    signals, not returned calls or unique tasks. Missing capacity series stay missing."""
    operations: list[ObsRuntimeAppTaskOperationSummary]
    incomplete: bool
    items: list[ObsRuntimeAppTaskOperationItem]
    omitted_items: int
    reason: ObsRuntimeAppTaskOperationsReason | Unset = UNSET
    comparison_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        state: str = self.state

        counts = self.counts.to_dict()

        operations = []
        for operations_item_data in self.operations:
            operations_item = operations_item_data.to_dict()
            operations.append(operations_item)

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
                "operations": operations,
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
        from ..models.obs_runtime_app_task_operation_counts import ObsRuntimeAppTaskOperationCounts
        from ..models.obs_runtime_app_task_operation_item import ObsRuntimeAppTaskOperationItem
        from ..models.obs_runtime_app_task_operation_summary import ObsRuntimeAppTaskOperationSummary

        d = dict(src_dict)
        scope = check_obs_runtime_app_task_operations_scope(d.pop("scope"))

        state = check_obs_runtime_app_task_operations_state(d.pop("state"))

        counts = ObsRuntimeAppTaskOperationCounts.from_dict(d.pop("counts"))

        operations = []
        _operations = d.pop("operations")
        for operations_item_data in _operations:
            operations_item = ObsRuntimeAppTaskOperationSummary.from_dict(operations_item_data)

            operations.append(operations_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeAppTaskOperationItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeAppTaskOperationsReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_app_task_operations_reason(_reason)

        _comparison_at = d.pop("comparison_at", UNSET)
        comparison_at: datetime.datetime | Unset
        if isinstance(_comparison_at, Unset):
            comparison_at = UNSET
        else:
            comparison_at = datetime.datetime.fromisoformat(_comparison_at)

        obs_runtime_app_task_operations = cls(
            scope=scope,
            state=state,
            counts=counts,
            operations=operations,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
            comparison_at=comparison_at,
        )

        obs_runtime_app_task_operations.additional_properties = d
        return obs_runtime_app_task_operations

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
