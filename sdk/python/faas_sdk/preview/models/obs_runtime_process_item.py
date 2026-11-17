from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_process_item_comparison import (
    ObsRuntimeProcessItemComparison,
    check_obs_runtime_process_item_comparison,
)
from ..models.obs_runtime_process_item_comparison_reason import (
    ObsRuntimeProcessItemComparisonReason,
    check_obs_runtime_process_item_comparison_reason,
)
from ..models.obs_runtime_process_item_reason import ObsRuntimeProcessItemReason, check_obs_runtime_process_item_reason
from ..models.obs_runtime_process_item_revision_relation import (
    ObsRuntimeProcessItemRevisionRelation,
    check_obs_runtime_process_item_revision_relation,
)
from ..models.obs_runtime_process_item_state import ObsRuntimeProcessItemState, check_obs_runtime_process_item_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeProcessItem")


@_attrs_define
class ObsRuntimeProcessItem:
    """Original uptime and sample clocks; comparison uses the same complete target identity and permits clock skew.
    Optional fields are absent for unavailable evidence.

    """

    state: ObsRuntimeProcessItemState
    reason: ObsRuntimeProcessItemReason | Unset = UNSET
    uptime_seconds: float | Unset = UNSET
    observed_at: datetime.datetime | Unset = UNSET
    comparison: ObsRuntimeProcessItemComparison | Unset = UNSET
    comparison_reason: ObsRuntimeProcessItemComparisonReason | Unset = UNSET
    previous_uptime_seconds: float | Unset = UNSET
    previous_observed_at: datetime.datetime | Unset = UNSET
    revision_relation: ObsRuntimeProcessItemRevisionRelation | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        uptime_seconds = self.uptime_seconds

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        comparison: str | Unset = UNSET
        if not isinstance(self.comparison, Unset):
            comparison = self.comparison

        comparison_reason: str | Unset = UNSET
        if not isinstance(self.comparison_reason, Unset):
            comparison_reason = self.comparison_reason

        previous_uptime_seconds = self.previous_uptime_seconds

        previous_observed_at: str | Unset = UNSET
        if not isinstance(self.previous_observed_at, Unset):
            previous_observed_at = self.previous_observed_at.isoformat()

        revision_relation: str | Unset = UNSET
        if not isinstance(self.revision_relation, Unset):
            revision_relation = self.revision_relation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if uptime_seconds is not UNSET:
            field_dict["uptime_seconds"] = uptime_seconds
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if comparison is not UNSET:
            field_dict["comparison"] = comparison
        if comparison_reason is not UNSET:
            field_dict["comparison_reason"] = comparison_reason
        if previous_uptime_seconds is not UNSET:
            field_dict["previous_uptime_seconds"] = previous_uptime_seconds
        if previous_observed_at is not UNSET:
            field_dict["previous_observed_at"] = previous_observed_at
        if revision_relation is not UNSET:
            field_dict["revision_relation"] = revision_relation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_obs_runtime_process_item_state(d.pop("state"))

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeProcessItemReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_process_item_reason(_reason)

        uptime_seconds = d.pop("uptime_seconds", UNSET)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _comparison = d.pop("comparison", UNSET)
        comparison: ObsRuntimeProcessItemComparison | Unset
        if isinstance(_comparison, Unset):
            comparison = UNSET
        else:
            comparison = check_obs_runtime_process_item_comparison(_comparison)

        _comparison_reason = d.pop("comparison_reason", UNSET)
        comparison_reason: ObsRuntimeProcessItemComparisonReason | Unset
        if isinstance(_comparison_reason, Unset):
            comparison_reason = UNSET
        else:
            comparison_reason = check_obs_runtime_process_item_comparison_reason(_comparison_reason)

        previous_uptime_seconds = d.pop("previous_uptime_seconds", UNSET)

        _previous_observed_at = d.pop("previous_observed_at", UNSET)
        previous_observed_at: datetime.datetime | Unset
        if isinstance(_previous_observed_at, Unset):
            previous_observed_at = UNSET
        else:
            previous_observed_at = datetime.datetime.fromisoformat(_previous_observed_at)

        _revision_relation = d.pop("revision_relation", UNSET)
        revision_relation: ObsRuntimeProcessItemRevisionRelation | Unset
        if isinstance(_revision_relation, Unset):
            revision_relation = UNSET
        else:
            revision_relation = check_obs_runtime_process_item_revision_relation(_revision_relation)

        obs_runtime_process_item = cls(
            state=state,
            reason=reason,
            uptime_seconds=uptime_seconds,
            observed_at=observed_at,
            comparison=comparison,
            comparison_reason=comparison_reason,
            previous_uptime_seconds=previous_uptime_seconds,
            previous_observed_at=previous_observed_at,
            revision_relation=revision_relation,
        )

        obs_runtime_process_item.additional_properties = d
        return obs_runtime_process_item

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
