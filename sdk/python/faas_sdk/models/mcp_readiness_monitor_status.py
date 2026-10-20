from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.mcp_readiness_monitor_status_recovery_action import (
    MCPReadinessMonitorStatusRecoveryAction,
    check_mcp_readiness_monitor_status_recovery_action,
)
from ..models.mcp_readiness_monitor_status_state import (
    MCPReadinessMonitorStatusState,
    check_mcp_readiness_monitor_status_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_monitor_observation import MCPReadinessMonitorObservation
    from ..models.mcp_readiness_o_auth_connection import MCPReadinessOAuthConnection


T = TypeVar("T", bound="MCPReadinessMonitorStatus")


@_attrs_define
class MCPReadinessMonitorStatus:
    """Current policy-bound monitoring state and recent probe history for a deployment role."""

    deployment_id: UUID
    role: str
    policy_revision: UUID
    next_probe_at: datetime.datetime
    consecutive_failures: int
    failure_threshold: int
    state: MCPReadinessMonitorStatusState
    history: list[MCPReadinessMonitorObservation]
    scope: str | Unset = UNSET
    """Readiness environment. Omission selects default."""
    oauth_connection: MCPReadinessOAuthConnection | Unset = UNSET
    """App-local managed OAuth connection in the default environment scope, pinned to its current revision."""
    recovery_action: MCPReadinessMonitorStatusRecoveryAction | Unset = UNSET
    artifact_sha256: str | Unset = UNSET
    last_success_at: datetime.datetime | Unset = UNSET
    reason: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        role = self.role

        policy_revision = str(self.policy_revision)

        next_probe_at = self.next_probe_at.isoformat()

        consecutive_failures = self.consecutive_failures

        failure_threshold = self.failure_threshold

        state: str = self.state

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        scope = self.scope

        oauth_connection: dict[str, Any] | Unset = UNSET
        if not isinstance(self.oauth_connection, Unset):
            oauth_connection = self.oauth_connection.to_dict()

        recovery_action: str | Unset = UNSET
        if not isinstance(self.recovery_action, Unset):
            recovery_action = self.recovery_action

        artifact_sha256 = self.artifact_sha256

        last_success_at: str | Unset = UNSET
        if not isinstance(self.last_success_at, Unset):
            last_success_at = self.last_success_at.isoformat()

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "deployment_id": deployment_id,
                "role": role,
                "policy_revision": policy_revision,
                "next_probe_at": next_probe_at,
                "consecutive_failures": consecutive_failures,
                "failure_threshold": failure_threshold,
                "state": state,
                "history": history,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if oauth_connection is not UNSET:
            field_dict["oauth_connection"] = oauth_connection
        if recovery_action is not UNSET:
            field_dict["recovery_action"] = recovery_action
        if artifact_sha256 is not UNSET:
            field_dict["artifact_sha256"] = artifact_sha256
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_monitor_observation import MCPReadinessMonitorObservation
        from ..models.mcp_readiness_o_auth_connection import MCPReadinessOAuthConnection

        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        role = d.pop("role")

        policy_revision = UUID(d.pop("policy_revision"))

        next_probe_at = datetime.datetime.fromisoformat(d.pop("next_probe_at"))

        consecutive_failures = d.pop("consecutive_failures")

        failure_threshold = d.pop("failure_threshold")

        state = check_mcp_readiness_monitor_status_state(d.pop("state"))

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = MCPReadinessMonitorObservation.from_dict(history_item_data)

            history.append(history_item)

        scope = d.pop("scope", UNSET)

        _oauth_connection = d.pop("oauth_connection", UNSET)
        oauth_connection: MCPReadinessOAuthConnection | Unset
        if isinstance(_oauth_connection, Unset):
            oauth_connection = UNSET
        else:
            oauth_connection = MCPReadinessOAuthConnection.from_dict(_oauth_connection)

        _recovery_action = d.pop("recovery_action", UNSET)
        recovery_action: MCPReadinessMonitorStatusRecoveryAction | Unset
        if isinstance(_recovery_action, Unset):
            recovery_action = UNSET
        else:
            recovery_action = check_mcp_readiness_monitor_status_recovery_action(_recovery_action)

        artifact_sha256 = d.pop("artifact_sha256", UNSET)

        _last_success_at = d.pop("last_success_at", UNSET)
        last_success_at: datetime.datetime | Unset
        if isinstance(_last_success_at, Unset):
            last_success_at = UNSET
        else:
            last_success_at = datetime.datetime.fromisoformat(_last_success_at)

        reason = d.pop("reason", UNSET)

        mcp_readiness_monitor_status = cls(
            deployment_id=deployment_id,
            role=role,
            policy_revision=policy_revision,
            next_probe_at=next_probe_at,
            consecutive_failures=consecutive_failures,
            failure_threshold=failure_threshold,
            state=state,
            history=history,
            scope=scope,
            oauth_connection=oauth_connection,
            recovery_action=recovery_action,
            artifact_sha256=artifact_sha256,
            last_success_at=last_success_at,
            reason=reason,
        )

        return mcp_readiness_monitor_status
