from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_readiness_item_reason import (
    ObsRuntimeReadinessItemReason,
    check_obs_runtime_readiness_item_reason,
)
from ..models.obs_runtime_readiness_item_state import (
    ObsRuntimeReadinessItemState,
    check_obs_runtime_readiness_item_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeReadinessItem")


@_attrs_define
class ObsRuntimeReadinessItem:
    """Original readiness and active reason sample clocks; ready requires a fresh successful scrape and complete consistent
    one-hot reasons.

    """

    state: ObsRuntimeReadinessItemState
    reason: ObsRuntimeReadinessItemReason
    observed_at: datetime.datetime | Unset = UNSET
    reason_observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        reason: str = self.reason

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        reason_observed_at: str | Unset = UNSET
        if not isinstance(self.reason_observed_at, Unset):
            reason_observed_at = self.reason_observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "reason": reason,
            }
        )
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if reason_observed_at is not UNSET:
            field_dict["reason_observed_at"] = reason_observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_obs_runtime_readiness_item_state(d.pop("state"))

        reason = check_obs_runtime_readiness_item_reason(d.pop("reason"))

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _reason_observed_at = d.pop("reason_observed_at", UNSET)
        reason_observed_at: datetime.datetime | Unset
        if isinstance(_reason_observed_at, Unset):
            reason_observed_at = UNSET
        else:
            reason_observed_at = datetime.datetime.fromisoformat(_reason_observed_at)

        obs_runtime_readiness_item = cls(
            state=state,
            reason=reason,
            observed_at=observed_at,
            reason_observed_at=reason_observed_at,
        )

        obs_runtime_readiness_item.additional_properties = d
        return obs_runtime_readiness_item

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
