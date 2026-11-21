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
from ..models.obs_runtime_preparation_phase_name import (
    ObsRuntimePreparationPhaseName,
    check_obs_runtime_preparation_phase_name,
)
from ..models.obs_runtime_preparation_phase_state import (
    ObsRuntimePreparationPhaseState,
    check_obs_runtime_preparation_phase_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_preparation_phase_values import ObsRuntimePreparationPhaseValues


T = TypeVar("T", bound="ObsRuntimePreparationPhase")


@_attrs_define
class ObsRuntimePreparationPhase:
    """One ordered durable phase. Usable values require successful fresh collection after the evidence boundary and the
    same original scrape clock as collection metadata. Missing or expired snapshots never become empty.

    """

    phase: ObsRuntimePreparationPhaseName
    """Durable preparation checkpoint phase, ordered preparing, layer_published, scan_complete. This is the last
    committed phase, including time waiting to resume, not the currently executing substep."""
    state: ObsRuntimePreparationPhaseState
    reason: ObsRuntimeHandoffEvidenceReason | Unset = UNSET
    """Closed reason for incomplete or unusable handoff evidence; collection prerequisites apply to backlog
    observations."""
    observed_at: datetime.datetime | Unset = UNSET
    values: ObsRuntimePreparationPhaseValues | Unset = UNSET
    """Pending active or recoverable checkpoints in this scope. Unknown age is included in pending. When pending
    exceeds unknown_age, oldest known timing is required; otherwise both optional timing fields are absent. Phase
    start may precede the rollout and includes time waiting to resume. Counts obey an exact-integer wire bound, not
    an admission quota."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        phase: str = self.phase

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
                "phase": phase,
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
        from ..models.obs_runtime_preparation_phase_values import ObsRuntimePreparationPhaseValues

        d = dict(src_dict)
        phase = check_obs_runtime_preparation_phase_name(d.pop("phase"))

        state = check_obs_runtime_preparation_phase_state(d.pop("state"))

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
        values: ObsRuntimePreparationPhaseValues | Unset
        if isinstance(_values, Unset):
            values = UNSET
        else:
            values = ObsRuntimePreparationPhaseValues.from_dict(_values)

        obs_runtime_preparation_phase = cls(
            phase=phase,
            state=state,
            reason=reason,
            observed_at=observed_at,
            values=values,
        )

        obs_runtime_preparation_phase.additional_properties = d
        return obs_runtime_preparation_phase

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
