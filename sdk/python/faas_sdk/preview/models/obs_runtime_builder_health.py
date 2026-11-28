from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_builder_health_reason import (
    ObsRuntimeBuilderHealthReason,
    check_obs_runtime_builder_health_reason,
)
from ..models.obs_runtime_builder_health_state import (
    ObsRuntimeBuilderHealthState,
    check_obs_runtime_builder_health_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_builder_counts import ObsRuntimeBuilderCounts
    from ..models.obs_runtime_builder_item import ObsRuntimeBuilderItem


T = TypeVar("T", bound="ObsRuntimeBuilderHealth")


@_attrs_define
class ObsRuntimeBuilderHealth:
    """Advisory build worker progress and process-local queue/slot pressure for explicit v2 profiles. Counts cover every
    inspected target before previewing. A fresh overdue worker remains visible with incomplete queue/slot evidence;
    saturation does not establish a build failure.

    """

    state: ObsRuntimeBuilderHealthState
    counts: ObsRuntimeBuilderCounts
    """Complete/missing/stale/conflicting/unavailable partition target observations. Worker states, waiting queues
    and saturated slot allocators overlap that partition and retain independently usable component evidence. These
    are process-local observations, not fleet capacity or successful builds."""
    incomplete: bool
    items: list[ObsRuntimeBuilderItem]
    omitted_items: int
    reason: ObsRuntimeBuilderHealthReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        counts = self.counts.to_dict()

        incomplete = self.incomplete

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        omitted_items = self.omitted_items

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "counts": counts,
                "incomplete": incomplete,
                "items": items,
                "omitted_items": omitted_items,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_builder_counts import ObsRuntimeBuilderCounts
        from ..models.obs_runtime_builder_item import ObsRuntimeBuilderItem

        d = dict(src_dict)
        state = check_obs_runtime_builder_health_state(d.pop("state"))

        counts = ObsRuntimeBuilderCounts.from_dict(d.pop("counts"))

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeBuilderItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeBuilderHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_builder_health_reason(_reason)

        obs_runtime_builder_health = cls(
            state=state,
            counts=counts,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
        )

        obs_runtime_builder_health.additional_properties = d
        return obs_runtime_builder_health

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
