from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_build_recovery_health_reason import (
    ObsRuntimeBuildRecoveryHealthReason,
    check_obs_runtime_build_recovery_health_reason,
)
from ..models.obs_runtime_build_recovery_health_scope import (
    ObsRuntimeBuildRecoveryHealthScope,
    check_obs_runtime_build_recovery_health_scope,
)
from ..models.obs_runtime_build_recovery_health_state import (
    ObsRuntimeBuildRecoveryHealthState,
    check_obs_runtime_build_recovery_health_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_build_recovery_counts import ObsRuntimeBuildRecoveryCounts
    from ..models.obs_runtime_build_recovery_item import ObsRuntimeBuildRecoveryItem


T = TypeVar("T", bound="ObsRuntimeBuildRecoveryHealth")


@_attrs_define
class ObsRuntimeBuildRecoveryHealth:
    """Advisory running build claims and durable VM cleanup obligations as independently collected API replica views of the
    shared fleet. Compare views without summing build or cleanup counts. Cleanup age begins at obligation creation and
    survives retries; evidence is not a VM liveness or deployment-readiness verdict.

    """

    scope: ObsRuntimeBuildRecoveryHealthScope
    state: ObsRuntimeBuildRecoveryHealthState
    counts: ObsRuntimeBuildRecoveryCounts
    """Complete/missing/stale/conflicting/unavailable partition replica observations. Remaining counts overlap that
    partition and count views, never builds or obligations. Running and cleanup views may overlap; nonempty is their
    union, empty requires both components observed empty, and retried cleanup views are a subset of cleanup views.
   """
    incomplete: bool
    items: list[ObsRuntimeBuildRecoveryItem]
    omitted_items: int
    reason: ObsRuntimeBuildRecoveryHealthReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

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
                "scope": scope,
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
        from ..models.obs_runtime_build_recovery_counts import ObsRuntimeBuildRecoveryCounts
        from ..models.obs_runtime_build_recovery_item import ObsRuntimeBuildRecoveryItem

        d = dict(src_dict)
        scope = check_obs_runtime_build_recovery_health_scope(d.pop("scope"))

        state = check_obs_runtime_build_recovery_health_state(d.pop("state"))

        counts = ObsRuntimeBuildRecoveryCounts.from_dict(d.pop("counts"))

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeBuildRecoveryItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeBuildRecoveryHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_build_recovery_health_reason(_reason)

        obs_runtime_build_recovery_health = cls(
            scope=scope,
            state=state,
            counts=counts,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
        )

        obs_runtime_build_recovery_health.additional_properties = d
        return obs_runtime_build_recovery_health

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
