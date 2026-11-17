from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_app_task_health_reason import (
    ObsRuntimeAppTaskHealthReason,
    check_obs_runtime_app_task_health_reason,
)
from ..models.obs_runtime_app_task_health_scope import (
    ObsRuntimeAppTaskHealthScope,
    check_obs_runtime_app_task_health_scope,
)
from ..models.obs_runtime_app_task_health_state import (
    ObsRuntimeAppTaskHealthState,
    check_obs_runtime_app_task_health_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_app_task_class_counts import ObsRuntimeAppTaskClassCounts
    from ..models.obs_runtime_app_task_counts import ObsRuntimeAppTaskCounts
    from ..models.obs_runtime_app_task_item import ObsRuntimeAppTaskItem


T = TypeVar("T", bound="ObsRuntimeAppTaskHealth")


@_attrs_define
class ObsRuntimeAppTaskHealth:
    """Advisory release, manual and cron task evidence for v2 scheduler jobs. Each scheduler observes the shared fleet
    independently. Compare views without summing task counts. Local worker slots do not measure fleet tasks or VMs. No
    deployment health or eligibility verdict is inferred.

    """

    scope: ObsRuntimeAppTaskHealthScope
    state: ObsRuntimeAppTaskHealthState
    counts: ObsRuntimeAppTaskCounts
    """Replica-view coverage and task evidence across the full inspected set before clipping. Nonempty means any
    positive task class; empty requires all 27 kind/class observations empty. Observer expectation and local
    dispatcher activity are independent. Running and stopped runner views partition dispatch-enabled views; occupied
    and saturated views describe local handler slots."""
    classes: list[ObsRuntimeAppTaskClassCounts]
    incomplete: bool
    items: list[ObsRuntimeAppTaskItem]
    omitted_items: int
    reason: ObsRuntimeAppTaskHealthReason | Unset = UNSET
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
        from ..models.obs_runtime_app_task_class_counts import ObsRuntimeAppTaskClassCounts
        from ..models.obs_runtime_app_task_counts import ObsRuntimeAppTaskCounts
        from ..models.obs_runtime_app_task_item import ObsRuntimeAppTaskItem

        d = dict(src_dict)
        scope = check_obs_runtime_app_task_health_scope(d.pop("scope"))

        state = check_obs_runtime_app_task_health_state(d.pop("state"))

        counts = ObsRuntimeAppTaskCounts.from_dict(d.pop("counts"))

        classes = []
        _classes = d.pop("classes")
        for classes_item_data in _classes:
            classes_item = ObsRuntimeAppTaskClassCounts.from_dict(classes_item_data)

            classes.append(classes_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeAppTaskItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeAppTaskHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_app_task_health_reason(_reason)

        obs_runtime_app_task_health = cls(
            scope=scope,
            state=state,
            counts=counts,
            classes=classes,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
        )

        obs_runtime_app_task_health.additional_properties = d
        return obs_runtime_app_task_health

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
