from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_canary_gate_decision_on_timeout import (
    ProfileCanaryGateDecisionOnTimeout,
    check_profile_canary_gate_decision_on_timeout,
)
from ..models.profile_canary_gate_decision_status import (
    ProfileCanaryGateDecisionStatus,
    check_profile_canary_gate_decision_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.canary_profile_signal import CanaryProfileSignal


T = TypeVar("T", bound="ProfileCanaryGateDecision")


@_attrs_define
class ProfileCanaryGateDecision:
    """Current profiling gate decision for an owned canary stage and its exact stable predecessor."""

    status: ProfileCanaryGateDecisionStatus
    reason: str
    policy_revision: int
    canary_step: int
    auto_rollback: bool
    canary_step_started_at: datetime.datetime | Unset = UNSET
    deadline: datetime.datetime | Unset = UNSET
    on_timeout: ProfileCanaryGateDecisionOnTimeout | Unset = UNSET
    stable_deployment_id: UUID | Unset = UNSET
    signal: CanaryProfileSignal | Unset = UNSET
    """Latest retained sampled CPU comparison for a deployment's canary stages, produced by a background worker
    using the app's enabled automatic profile policy and equal fixed windows. The stage and policy revision identify
    the assessment. Route-health reads return the saved result and never query profile storage. Checks are advisory
    unless canary_gate is explicitly configured. Gate evidence requires consecutive distinct qualified route
    windows; timeout behavior is configured explicitly. Completed assessments are retained for 30 days."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        policy_revision = self.policy_revision

        canary_step = self.canary_step

        auto_rollback = self.auto_rollback

        canary_step_started_at: str | Unset = UNSET
        if not isinstance(self.canary_step_started_at, Unset):
            canary_step_started_at = self.canary_step_started_at.isoformat()

        deadline: str | Unset = UNSET
        if not isinstance(self.deadline, Unset):
            deadline = self.deadline.isoformat()

        on_timeout: str | Unset = UNSET
        if not isinstance(self.on_timeout, Unset):
            on_timeout = self.on_timeout

        stable_deployment_id: str | Unset = UNSET
        if not isinstance(self.stable_deployment_id, Unset):
            stable_deployment_id = str(self.stable_deployment_id)

        signal: dict[str, Any] | Unset = UNSET
        if not isinstance(self.signal, Unset):
            signal = self.signal.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "reason": reason,
                "policy_revision": policy_revision,
                "canary_step": canary_step,
                "auto_rollback": auto_rollback,
            }
        )
        if canary_step_started_at is not UNSET:
            field_dict["canary_step_started_at"] = canary_step_started_at
        if deadline is not UNSET:
            field_dict["deadline"] = deadline
        if on_timeout is not UNSET:
            field_dict["on_timeout"] = on_timeout
        if stable_deployment_id is not UNSET:
            field_dict["stable_deployment_id"] = stable_deployment_id
        if signal is not UNSET:
            field_dict["signal"] = signal

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.canary_profile_signal import CanaryProfileSignal

        d = dict(src_dict)
        status = check_profile_canary_gate_decision_status(d.pop("status"))

        reason = d.pop("reason")

        policy_revision = d.pop("policy_revision")

        canary_step = d.pop("canary_step")

        auto_rollback = d.pop("auto_rollback")

        _canary_step_started_at = d.pop("canary_step_started_at", UNSET)
        canary_step_started_at: datetime.datetime | Unset
        if isinstance(_canary_step_started_at, Unset):
            canary_step_started_at = UNSET
        else:
            canary_step_started_at = datetime.datetime.fromisoformat(_canary_step_started_at)

        _deadline = d.pop("deadline", UNSET)
        deadline: datetime.datetime | Unset
        if isinstance(_deadline, Unset):
            deadline = UNSET
        else:
            deadline = datetime.datetime.fromisoformat(_deadline)

        _on_timeout = d.pop("on_timeout", UNSET)
        on_timeout: ProfileCanaryGateDecisionOnTimeout | Unset
        if isinstance(_on_timeout, Unset):
            on_timeout = UNSET
        else:
            on_timeout = check_profile_canary_gate_decision_on_timeout(_on_timeout)

        _stable_deployment_id = d.pop("stable_deployment_id", UNSET)
        stable_deployment_id: UUID | Unset
        if isinstance(_stable_deployment_id, Unset):
            stable_deployment_id = UNSET
        else:
            stable_deployment_id = UUID(_stable_deployment_id)

        _signal = d.pop("signal", UNSET)
        signal: CanaryProfileSignal | Unset
        if isinstance(_signal, Unset):
            signal = UNSET
        else:
            signal = CanaryProfileSignal.from_dict(_signal)

        profile_canary_gate_decision = cls(
            status=status,
            reason=reason,
            policy_revision=policy_revision,
            canary_step=canary_step,
            auto_rollback=auto_rollback,
            canary_step_started_at=canary_step_started_at,
            deadline=deadline,
            on_timeout=on_timeout,
            stable_deployment_id=stable_deployment_id,
            signal=signal,
        )

        profile_canary_gate_decision.additional_properties = d
        return profile_canary_gate_decision

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
