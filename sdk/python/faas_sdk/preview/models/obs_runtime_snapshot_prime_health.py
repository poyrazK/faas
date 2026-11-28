from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_snapshot_prime_health_reason import (
    ObsRuntimeSnapshotPrimeHealthReason,
    check_obs_runtime_snapshot_prime_health_reason,
)
from ..models.obs_runtime_snapshot_prime_health_state import (
    ObsRuntimeSnapshotPrimeHealthState,
    check_obs_runtime_snapshot_prime_health_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_snapshot_prime_class_counts import ObsRuntimeSnapshotPrimeClassCounts
    from ..models.obs_runtime_snapshot_prime_counts import ObsRuntimeSnapshotPrimeCounts
    from ..models.obs_runtime_snapshot_prime_item import ObsRuntimeSnapshotPrimeItem


T = TypeVar("T", bound="ObsRuntimeSnapshotPrimeHealth")


@_attrs_define
class ObsRuntimeSnapshotPrimeHealth:
    """Advisory snapshot-priming evidence for scheduler jobs selected by v2 profiles. Ownership scopes may overlap; never
    sum attempt counts across replicas. Active or recorded snapshots do not establish deployment readiness.

    """

    state: ObsRuntimeSnapshotPrimeHealthState
    counts: ObsRuntimeSnapshotPrimeCounts
    """Complete/missing/stale/conflicting/unavailable partition inspected scheduler targets. Other counts overlap
    this partition and describe observation scopes. Nonempty scopes have any current attempts, including active or
    snapshot-recorded classes; empty requires all six classes known empty. Full component availability can include
    unknown ages."""
    classes: list[ObsRuntimeSnapshotPrimeClassCounts]
    incomplete: bool
    items: list[ObsRuntimeSnapshotPrimeItem]
    omitted_items: int
    reason: ObsRuntimeSnapshotPrimeHealthReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        counts = self.counts.to_dict()

        classes = []
        for classes_item_data in self.classes:
            classes_item = classes_item_data.to_dict()
            classes.append(classes_item)

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
                "classes": classes,
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
        from ..models.obs_runtime_snapshot_prime_class_counts import ObsRuntimeSnapshotPrimeClassCounts
        from ..models.obs_runtime_snapshot_prime_counts import ObsRuntimeSnapshotPrimeCounts
        from ..models.obs_runtime_snapshot_prime_item import ObsRuntimeSnapshotPrimeItem

        d = dict(src_dict)
        state = check_obs_runtime_snapshot_prime_health_state(d.pop("state"))

        counts = ObsRuntimeSnapshotPrimeCounts.from_dict(d.pop("counts"))

        classes = []
        _classes = d.pop("classes")
        for classes_item_data in _classes:
            classes_item = ObsRuntimeSnapshotPrimeClassCounts.from_dict(classes_item_data)

            classes.append(classes_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeSnapshotPrimeItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeSnapshotPrimeHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_snapshot_prime_health_reason(_reason)

        obs_runtime_snapshot_prime_health = cls(
            state=state,
            counts=counts,
            classes=classes,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
        )

        obs_runtime_snapshot_prime_health.additional_properties = d
        return obs_runtime_snapshot_prime_health

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
