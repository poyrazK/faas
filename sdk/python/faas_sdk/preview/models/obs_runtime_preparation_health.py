from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_preparation_health_reason import (
    ObsRuntimePreparationHealthReason,
    check_obs_runtime_preparation_health_reason,
)
from ..models.obs_runtime_preparation_health_state import (
    ObsRuntimePreparationHealthState,
    check_obs_runtime_preparation_health_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_preparation_counts import ObsRuntimePreparationCounts
    from ..models.obs_runtime_preparation_item import ObsRuntimePreparationItem
    from ..models.obs_runtime_preparation_phase_counts import ObsRuntimePreparationPhaseCounts


T = TypeVar("T", bound="ObsRuntimePreparationHealth")


@_attrs_define
class ObsRuntimePreparationHealth:
    """Advisory durable image-preparation evidence for image workers in v2 profiles. Counts describe observation scopes,
    which may overlap across replicas. Positive phase evidence survives gaps in other components. No execution deadline
    or deployment success is inferred.

    """

    state: ObsRuntimePreparationHealthState
    counts: ObsRuntimePreparationCounts
    """Complete/missing/stale/conflicting/unavailable partition targets. Remaining counts overlap that partition.
    Complete means usable components; historical ages may still be unknown. Waiting scopes have any observed pending
    phase; empty scopes have all three phases known empty. Disabled observation never implies drained work."""
    phases: list[ObsRuntimePreparationPhaseCounts]
    incomplete: bool
    items: list[ObsRuntimePreparationItem]
    omitted_items: int
    reason: ObsRuntimePreparationHealthReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        counts = self.counts.to_dict()

        phases = []
        for phases_item_data in self.phases:
            phases_item = phases_item_data.to_dict()
            phases.append(phases_item)

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
                "phases": phases,
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
        from ..models.obs_runtime_preparation_counts import ObsRuntimePreparationCounts
        from ..models.obs_runtime_preparation_item import ObsRuntimePreparationItem
        from ..models.obs_runtime_preparation_phase_counts import ObsRuntimePreparationPhaseCounts

        d = dict(src_dict)
        state = check_obs_runtime_preparation_health_state(d.pop("state"))

        counts = ObsRuntimePreparationCounts.from_dict(d.pop("counts"))

        phases = []
        _phases = d.pop("phases")
        for phases_item_data in _phases:
            phases_item = ObsRuntimePreparationPhaseCounts.from_dict(phases_item_data)

            phases.append(phases_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimePreparationItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimePreparationHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_preparation_health_reason(_reason)

        obs_runtime_preparation_health = cls(
            state=state,
            counts=counts,
            phases=phases,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
        )

        obs_runtime_preparation_health.additional_properties = d
        return obs_runtime_preparation_health

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
