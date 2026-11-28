from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_handoff_evidence_reason import (
    ObsRuntimeHandoffEvidenceReason,
    check_obs_runtime_handoff_evidence_reason,
)
from ..models.obs_runtime_handoff_recovery_state import (
    ObsRuntimeHandoffRecoveryState,
    check_obs_runtime_handoff_recovery_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_handoff_recovery_values import ObsRuntimeHandoffRecoveryValues


T = TypeVar("T", bound="ObsRuntimeHandoffRecovery")


@_attrs_define
class ObsRuntimeHandoffRecovery:
    """Sampled recovery-pass activity with no new progress timeout. Synchronous image work can legitimately occupy a long
    pass; stopping can retain an in-flight pass and does not prove drained work.

    """

    state: ObsRuntimeHandoffRecoveryState
    reason: ObsRuntimeHandoffEvidenceReason | Unset = UNSET
    """Closed reason for incomplete or unusable handoff evidence; collection prerequisites apply to backlog
    observations."""
    observed_at: datetime.datetime | Unset = UNSET
    values: ObsRuntimeHandoffRecoveryValues | Unset = UNSET
    """Whether recovery is expected and inside a pass, with its latest attempt/return clocks. Returning includes
    failed and no-op passes and does not prove a committed checkpoint, successful preparation or deployment
    readiness."""
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
        from ..models.obs_runtime_handoff_recovery_values import ObsRuntimeHandoffRecoveryValues

        d = dict(src_dict)
        state = check_obs_runtime_handoff_recovery_state(d.pop("state"))

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeHandoffEvidenceReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_handoff_evidence_reason(_reason)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _values = d.pop("values", UNSET)
        values: ObsRuntimeHandoffRecoveryValues | Unset
        if isinstance(_values, Unset):
            values = UNSET
        else:
            values = ObsRuntimeHandoffRecoveryValues.from_dict(_values)

        obs_runtime_handoff_recovery = cls(
            state=state,
            reason=reason,
            observed_at=observed_at,
            values=values,
        )

        obs_runtime_handoff_recovery.additional_properties = d
        return obs_runtime_handoff_recovery

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
