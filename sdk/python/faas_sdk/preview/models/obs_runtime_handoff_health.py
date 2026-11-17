from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_handoff_health_reason import (
    ObsRuntimeHandoffHealthReason,
    check_obs_runtime_handoff_health_reason,
)
from ..models.obs_runtime_handoff_health_state import (
    ObsRuntimeHandoffHealthState,
    check_obs_runtime_handoff_health_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_handoff_counts import ObsRuntimeHandoffCounts
    from ..models.obs_runtime_handoff_item import ObsRuntimeHandoffItem


T = TypeVar("T", bound="ObsRuntimeHandoffHealth")


@_attrs_define
class ObsRuntimeHandoffHealth:
    """Advisory build-to-image handoff evidence for selected image workers in v2 profiles. Replica recovery scopes can
    overlap; pending values are never summed into a fleet total. Positive waiting evidence survives incomplete recovery
    metadata.

    """

    state: ObsRuntimeHandoffHealthState
    counts: ObsRuntimeHandoffCounts
    """Complete/missing/stale/conflicting/unavailable partition inspected targets. Backlog and recovery counts
    overlap that partition and count observation scopes, not unique builds. Unsuccessful collections count fresh
    zero success reports, including observers without any successful read yet."""
    incomplete: bool
    items: list[ObsRuntimeHandoffItem]
    omitted_items: int
    reason: ObsRuntimeHandoffHealthReason | Unset = UNSET
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
        from ..models.obs_runtime_handoff_counts import ObsRuntimeHandoffCounts
        from ..models.obs_runtime_handoff_item import ObsRuntimeHandoffItem

        d = dict(src_dict)
        state = check_obs_runtime_handoff_health_state(d.pop("state"))

        counts = ObsRuntimeHandoffCounts.from_dict(d.pop("counts"))

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeHandoffItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeHandoffHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_handoff_health_reason(_reason)

        obs_runtime_handoff_health = cls(
            state=state,
            counts=counts,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
        )

        obs_runtime_handoff_health.additional_properties = d
        return obs_runtime_handoff_health

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
