from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_builder_evidence_reason import (
    ObsRuntimeBuilderEvidenceReason,
    check_obs_runtime_builder_evidence_reason,
)
from ..models.obs_runtime_builder_slots_state import ObsRuntimeBuilderSlotsState, check_obs_runtime_builder_slots_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_builder_slot_values import ObsRuntimeBuilderSlotValues


T = TypeVar("T", bound="ObsRuntimeBuilderSlots")


@_attrs_define
class ObsRuntimeBuilderSlots:
    """Process-local reserved build slots; zero usage does not establish free host capacity. Observed_at is the oldest
    original sample clock of the two gauges.

    """

    state: ObsRuntimeBuilderSlotsState
    reason: ObsRuntimeBuilderEvidenceReason | Unset = UNSET
    """Closed diagnostic reason for missing, stale, conflicting or unusable builder evidence."""
    observed_at: datetime.datetime | Unset = UNSET
    values: ObsRuntimeBuilderSlotValues | Unset = UNSET
    """Current reserved slots and the enforced process-local limit; equality describes saturation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        values: dict[str, Any] | Unset = UNSET
        if not isinstance(self.values, Unset):
            values = self.values.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if values is not UNSET:
            field_dict["values"] = values

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_builder_slot_values import ObsRuntimeBuilderSlotValues

        d = dict(src_dict)
        state = check_obs_runtime_builder_slots_state(d.pop("state"))

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeBuilderEvidenceReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_builder_evidence_reason(_reason)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _values = d.pop("values", UNSET)
        values: ObsRuntimeBuilderSlotValues | Unset
        if isinstance(_values, Unset):
            values = UNSET
        else:
            values = ObsRuntimeBuilderSlotValues.from_dict(_values)

        obs_runtime_builder_slots = cls(
            state=state,
            reason=reason,
            observed_at=observed_at,
            values=values,
        )

        obs_runtime_builder_slots.additional_properties = d
        return obs_runtime_builder_slots

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
