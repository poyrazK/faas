from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_command_cron_health_reason import (
    ObsRuntimeCommandCronHealthReason,
    check_obs_runtime_command_cron_health_reason,
)
from ..models.obs_runtime_command_cron_health_scope import (
    ObsRuntimeCommandCronHealthScope,
    check_obs_runtime_command_cron_health_scope,
)
from ..models.obs_runtime_command_cron_health_state import (
    ObsRuntimeCommandCronHealthState,
    check_obs_runtime_command_cron_health_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_command_cron_class_counts import ObsRuntimeCommandCronClassCounts
    from ..models.obs_runtime_command_cron_counts import ObsRuntimeCommandCronCounts
    from ..models.obs_runtime_command_cron_item import ObsRuntimeCommandCronItem


T = TypeVar("T", bound="ObsRuntimeCommandCronHealth")


@_attrs_define
class ObsRuntimeCommandCronHealth:
    """Advisory command-cron schedule and sweep evidence for v2 scheduler jobs. Ownership scopes may overlap; compare
    scopes without summing schedules. Nonempty includes intentional inactive schedules. Due evidence describes an
    unconsumed nominal boundary, not a health, eligibility, admission or execution verdict.

    """

    scope: ObsRuntimeCommandCronHealthScope
    state: ObsRuntimeCommandCronHealthState
    counts: ObsRuntimeCommandCronCounts
    """Full-set scheduler-scope coverage, including overlapping scopes. Nonempty requires any observed schedule,
    including disabled or not-due intent; empty requires all ten classes observed empty. Due scopes contain any
    first-five class schedules; inactive scopes contain disabled, suspended, account-held or not-due schedules.
    Those categories can overlap. Sweep categories describe observed scan activity, not command delivery."""
    classes: list[ObsRuntimeCommandCronClassCounts]
    incomplete: bool
    items: list[ObsRuntimeCommandCronItem]
    omitted_items: int
    reason: ObsRuntimeCommandCronHealthReason | Unset = UNSET
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
        from ..models.obs_runtime_command_cron_class_counts import ObsRuntimeCommandCronClassCounts
        from ..models.obs_runtime_command_cron_counts import ObsRuntimeCommandCronCounts
        from ..models.obs_runtime_command_cron_item import ObsRuntimeCommandCronItem

        d = dict(src_dict)
        scope = check_obs_runtime_command_cron_health_scope(d.pop("scope"))

        state = check_obs_runtime_command_cron_health_state(d.pop("state"))

        counts = ObsRuntimeCommandCronCounts.from_dict(d.pop("counts"))

        classes = []
        _classes = d.pop("classes")
        for classes_item_data in _classes:
            classes_item = ObsRuntimeCommandCronClassCounts.from_dict(classes_item_data)

            classes.append(classes_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeCommandCronItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeCommandCronHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_command_cron_health_reason(_reason)

        obs_runtime_command_cron_health = cls(
            scope=scope,
            state=state,
            counts=counts,
            classes=classes,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
        )

        obs_runtime_command_cron_health.additional_properties = d
        return obs_runtime_command_cron_health

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
