from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_loop_health_reason import ObsRuntimeLoopHealthReason, check_obs_runtime_loop_health_reason
from ..models.obs_runtime_loop_health_state import ObsRuntimeLoopHealthState, check_obs_runtime_loop_health_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_loop_counts import ObsRuntimeLoopCounts
    from ..models.obs_runtime_loop_item import ObsRuntimeLoopItem


T = TypeVar("T", bound="ObsRuntimeLoopHealth")


@_attrs_define
class ObsRuntimeLoopHealth:
    """Advisory reports from expected runtime and work loops; fresh reported stalls remain visible when age evidence is
    incomplete. Counts cover all inspected target-loop pairs before the bounded preview.

    """

    state: ObsRuntimeLoopHealthState
    counts: ObsRuntimeLoopCounts
    """State counts partition inspected target-loop pairs. Expected and observed counts overlap the partition;
    reported_stalls includes fresh stall reports with incomplete age evidence."""
    incomplete: bool
    items: list[ObsRuntimeLoopItem]
    omitted_items: int
    reason: ObsRuntimeLoopHealthReason | Unset = UNSET
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
        from ..models.obs_runtime_loop_counts import ObsRuntimeLoopCounts
        from ..models.obs_runtime_loop_item import ObsRuntimeLoopItem

        d = dict(src_dict)
        state = check_obs_runtime_loop_health_state(d.pop("state"))

        counts = ObsRuntimeLoopCounts.from_dict(d.pop("counts"))

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeLoopItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeLoopHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_loop_health_reason(_reason)

        obs_runtime_loop_health = cls(
            state=state,
            counts=counts,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
        )

        obs_runtime_loop_health.additional_properties = d
        return obs_runtime_loop_health

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
