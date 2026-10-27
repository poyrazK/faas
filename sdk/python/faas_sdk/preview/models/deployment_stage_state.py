from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_stage_state_current import DeploymentStageStateCurrent, check_deployment_stage_state_current
from ..models.deployment_stage_state_retry_requested_stage import (
    DeploymentStageStateRetryRequestedStage,
    check_deployment_stage_state_retry_requested_stage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deployment_build_dependency_gate import DeploymentBuildDependencyGate
    from ..models.deployment_dependency_gate import DeploymentDependencyGate
    from ..models.deployment_hosting_verification import DeploymentHostingVerification
    from ..models.deployment_stage_history_entry import DeploymentStageHistoryEntry


T = TypeVar("T", bound="DeploymentStageState")


@_attrs_define
class DeploymentStageState:
    """Customer-visible deployment stage progress. `current` is the open stage, or an empty string once the deployment is
    live, failed or cancelled and that stage has moved into `history`.

    """

    current: DeploymentStageStateCurrent
    history: list[DeploymentStageHistoryEntry]
    """Closed stages, oldest first. Writers keep the most recent 64 (MaxStageHistory)."""
    current_started_at: datetime.datetime | None | Unset = UNSET
    """Start of the open stage. Null or absent when no stage is open, and on legacy rows created before stage
    timing."""
    retry_requested_stage: DeploymentStageStateRetryRequestedStage | Unset = UNSET
    """Stage a retry asked to resume from. Present only on retry deployments, which always restart at
    source_download."""
    retry_restart_reason: str | Unset = UNSET
    """Why a retry restarted from source_download rather than retry_requested_stage."""
    dependency_gate: DeploymentDependencyGate | Unset = UNSET
    """Compose dependency readiness gate pinned at admission. The deadline starts when the candidate first presents
    its own readiness proof."""
    build_dependency_gate: DeploymentBuildDependencyGate | Unset = UNSET
    """Build-time wait on the API deployments a guided service migration selected. It is checked before cache
    lookup and slot allocation and never holds a build slot while waiting."""
    hosting_verification: DeploymentHostingVerification | Unset = UNSET
    """Candidate hosting verification recovery progress. last_error_code distinguishes publication, gateway,
    transport and candidate-evidence failures; a transport failure does not attribute blame to the app or platform.
    retry_not_before is an eligibility floor, not a promised delivery time; completed_at means the attempt finished,
    while the hosting receipt records its verdict."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        current: str = self.current

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        current_started_at: None | str | Unset
        if isinstance(self.current_started_at, Unset):
            current_started_at = UNSET
        elif isinstance(self.current_started_at, datetime.datetime):
            current_started_at = self.current_started_at.isoformat()
        else:
            current_started_at = self.current_started_at

        retry_requested_stage: str | Unset = UNSET
        if not isinstance(self.retry_requested_stage, Unset):
            retry_requested_stage = self.retry_requested_stage

        retry_restart_reason = self.retry_restart_reason

        dependency_gate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.dependency_gate, Unset):
            dependency_gate = self.dependency_gate.to_dict()

        build_dependency_gate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.build_dependency_gate, Unset):
            build_dependency_gate = self.build_dependency_gate.to_dict()

        hosting_verification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.hosting_verification, Unset):
            hosting_verification = self.hosting_verification.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "current": current,
                "history": history,
            }
        )
        if current_started_at is not UNSET:
            field_dict["current_started_at"] = current_started_at
        if retry_requested_stage is not UNSET:
            field_dict["retry_requested_stage"] = retry_requested_stage
        if retry_restart_reason is not UNSET:
            field_dict["retry_restart_reason"] = retry_restart_reason
        if dependency_gate is not UNSET:
            field_dict["dependency_gate"] = dependency_gate
        if build_dependency_gate is not UNSET:
            field_dict["build_dependency_gate"] = build_dependency_gate
        if hosting_verification is not UNSET:
            field_dict["hosting_verification"] = hosting_verification

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_build_dependency_gate import DeploymentBuildDependencyGate
        from ..models.deployment_dependency_gate import DeploymentDependencyGate
        from ..models.deployment_hosting_verification import DeploymentHostingVerification
        from ..models.deployment_stage_history_entry import DeploymentStageHistoryEntry

        d = dict(src_dict)
        current = check_deployment_stage_state_current(d.pop("current"))

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = DeploymentStageHistoryEntry.from_dict(history_item_data)

            history.append(history_item)

        def _parse_current_started_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                current_started_at_type_0 = datetime.datetime.fromisoformat(data)

                return current_started_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        current_started_at = _parse_current_started_at(d.pop("current_started_at", UNSET))

        _retry_requested_stage = d.pop("retry_requested_stage", UNSET)
        retry_requested_stage: DeploymentStageStateRetryRequestedStage | Unset
        if isinstance(_retry_requested_stage, Unset):
            retry_requested_stage = UNSET
        else:
            retry_requested_stage = check_deployment_stage_state_retry_requested_stage(_retry_requested_stage)

        retry_restart_reason = d.pop("retry_restart_reason", UNSET)

        _dependency_gate = d.pop("dependency_gate", UNSET)
        dependency_gate: DeploymentDependencyGate | Unset
        if isinstance(_dependency_gate, Unset):
            dependency_gate = UNSET
        else:
            dependency_gate = DeploymentDependencyGate.from_dict(_dependency_gate)

        _build_dependency_gate = d.pop("build_dependency_gate", UNSET)
        build_dependency_gate: DeploymentBuildDependencyGate | Unset
        if isinstance(_build_dependency_gate, Unset):
            build_dependency_gate = UNSET
        else:
            build_dependency_gate = DeploymentBuildDependencyGate.from_dict(_build_dependency_gate)

        _hosting_verification = d.pop("hosting_verification", UNSET)
        hosting_verification: DeploymentHostingVerification | Unset
        if isinstance(_hosting_verification, Unset):
            hosting_verification = UNSET
        else:
            hosting_verification = DeploymentHostingVerification.from_dict(_hosting_verification)

        deployment_stage_state = cls(
            current=current,
            history=history,
            current_started_at=current_started_at,
            retry_requested_stage=retry_requested_stage,
            retry_restart_reason=retry_restart_reason,
            dependency_gate=dependency_gate,
            build_dependency_gate=build_dependency_gate,
            hosting_verification=hosting_verification,
        )

        deployment_stage_state.additional_properties = d
        return deployment_stage_state

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
