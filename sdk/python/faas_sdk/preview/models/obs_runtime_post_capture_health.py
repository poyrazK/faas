from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_post_capture_health_reason import (
    ObsRuntimePostCaptureHealthReason,
    check_obs_runtime_post_capture_health_reason,
)
from ..models.obs_runtime_post_capture_health_scope import (
    ObsRuntimePostCaptureHealthScope,
    check_obs_runtime_post_capture_health_scope,
)
from ..models.obs_runtime_post_capture_health_state import (
    ObsRuntimePostCaptureHealthState,
    check_obs_runtime_post_capture_health_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_post_capture_class_counts import ObsRuntimePostCaptureClassCounts
    from ..models.obs_runtime_post_capture_counts import ObsRuntimePostCaptureCounts
    from ..models.obs_runtime_post_capture_item import ObsRuntimePostCaptureItem


T = TypeVar("T", bound="ObsRuntimePostCaptureHealth")


@_attrs_define
class ObsRuntimePostCaptureHealth:
    """Advisory post-capture activation evidence for image workers in v2 profiles. Each replica reads the shared fleet
    independently; compare its view without summing deployment counts or inferring consensus. Gate classifications and
    ages are retained evidence, not a new eligibility or health verdict.

    """

    scope: ObsRuntimePostCaptureHealthScope
    state: ObsRuntimePostCaptureHealthState
    counts: ObsRuntimePostCaptureCounts
    """Complete/missing/stale/conflicting/unavailable partition replica observations. Other counts overlap the
    partition and count replica views, not deployments. Nonempty means any reported durable class evidence; empty
    requires all eight classes observed empty. Complete component evidence can contain unknown ages. Conflicting
    means inconsistent source series, not disagreement between replicas."""
    classes: list[ObsRuntimePostCaptureClassCounts]
    incomplete: bool
    items: list[ObsRuntimePostCaptureItem]
    omitted_items: int
    reason: ObsRuntimePostCaptureHealthReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

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
                "scope": scope,
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
        from ..models.obs_runtime_post_capture_class_counts import ObsRuntimePostCaptureClassCounts
        from ..models.obs_runtime_post_capture_counts import ObsRuntimePostCaptureCounts
        from ..models.obs_runtime_post_capture_item import ObsRuntimePostCaptureItem

        d = dict(src_dict)
        scope = check_obs_runtime_post_capture_health_scope(d.pop("scope"))

        state = check_obs_runtime_post_capture_health_state(d.pop("state"))

        counts = ObsRuntimePostCaptureCounts.from_dict(d.pop("counts"))

        classes = []
        _classes = d.pop("classes")
        for classes_item_data in _classes:
            classes_item = ObsRuntimePostCaptureClassCounts.from_dict(classes_item_data)

            classes.append(classes_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimePostCaptureItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimePostCaptureHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_post_capture_health_reason(_reason)

        obs_runtime_post_capture_health = cls(
            scope=scope,
            state=state,
            counts=counts,
            classes=classes,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
        )

        obs_runtime_post_capture_health.additional_properties = d
        return obs_runtime_post_capture_health

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
