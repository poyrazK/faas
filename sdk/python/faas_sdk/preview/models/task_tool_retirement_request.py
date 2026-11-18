from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.task_tool_retirement_request_action import (
    TaskToolRetirementRequestAction,
    check_task_tool_retirement_request_action,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_access_policy_expectation import MCPAccessPolicyExpectation
    from ..models.task_tool_recovery_qualification import TaskToolRecoveryQualification
    from ..models.task_tool_retirement_request_expected_instance_ids import TaskToolRetirementRequestExpectedInstanceIds


T = TypeVar("T", bound="TaskToolRetirementRequest")


@_attrs_define
class TaskToolRetirementRequest:
    """Private trusted CLI qualification, native park or read-only shutdown observation. Agent tools accept public
    identities only. Pending replays never issue a new park request.

    """

    action: TaskToolRetirementRequestAction | Unset = UNSET
    qualification: TaskToolRecoveryQualification | Unset = UNSET
    """Owner attestation of fresh local checks, exact recovery target and a locked native snapshot. The fixed
    deadline is observed_at plus 60 seconds, independently of the original release approval. Qualifying or approving
    never renews it."""
    qualification_sha256: str | Unset = UNSET
    claim_token: str | Unset = UNSET
    bindings_sha256: str | Unset = UNSET
    evidence_sha256: str | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    shutdown_timeout_ms: int | Unset = UNSET
    expected_instance_ids: TaskToolRetirementRequestExpectedInstanceIds | Unset = UNSET
    expected_mcp_access_policy: MCPAccessPolicyExpectation | Unset = UNSET
    """Native stored-policy concurrency condition for the exact canonical MCP resource, optionally including the
    converged account revision. It does not replace runtime authorization checks."""

    def to_dict(self) -> dict[str, Any]:
        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        qualification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.qualification, Unset):
            qualification = self.qualification.to_dict()

        qualification_sha256 = self.qualification_sha256

        claim_token = self.claim_token

        bindings_sha256 = self.bindings_sha256

        evidence_sha256 = self.evidence_sha256

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        shutdown_timeout_ms = self.shutdown_timeout_ms

        expected_instance_ids: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_instance_ids, Unset):
            expected_instance_ids = self.expected_instance_ids.to_dict()

        expected_mcp_access_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_mcp_access_policy, Unset):
            expected_mcp_access_policy = self.expected_mcp_access_policy.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if action is not UNSET:
            field_dict["action"] = action
        if qualification is not UNSET:
            field_dict["qualification"] = qualification
        if qualification_sha256 is not UNSET:
            field_dict["qualification_sha256"] = qualification_sha256
        if claim_token is not UNSET:
            field_dict["claim_token"] = claim_token
        if bindings_sha256 is not UNSET:
            field_dict["bindings_sha256"] = bindings_sha256
        if evidence_sha256 is not UNSET:
            field_dict["evidence_sha256"] = evidence_sha256
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if shutdown_timeout_ms is not UNSET:
            field_dict["shutdown_timeout_ms"] = shutdown_timeout_ms
        if expected_instance_ids is not UNSET:
            field_dict["expected_instance_ids"] = expected_instance_ids
        if expected_mcp_access_policy is not UNSET:
            field_dict["expected_mcp_access_policy"] = expected_mcp_access_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_access_policy_expectation import MCPAccessPolicyExpectation
        from ..models.task_tool_recovery_qualification import TaskToolRecoveryQualification
        from ..models.task_tool_retirement_request_expected_instance_ids import (
            TaskToolRetirementRequestExpectedInstanceIds,
        )

        d = dict(src_dict)
        _action = d.pop("action", UNSET)
        action: TaskToolRetirementRequestAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_task_tool_retirement_request_action(_action)

        _qualification = d.pop("qualification", UNSET)
        qualification: TaskToolRecoveryQualification | Unset
        if isinstance(_qualification, Unset):
            qualification = UNSET
        else:
            qualification = TaskToolRecoveryQualification.from_dict(_qualification)

        qualification_sha256 = d.pop("qualification_sha256", UNSET)

        claim_token = d.pop("claim_token", UNSET)

        bindings_sha256 = d.pop("bindings_sha256", UNSET)

        evidence_sha256 = d.pop("evidence_sha256", UNSET)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        shutdown_timeout_ms = d.pop("shutdown_timeout_ms", UNSET)

        _expected_instance_ids = d.pop("expected_instance_ids", UNSET)
        expected_instance_ids: TaskToolRetirementRequestExpectedInstanceIds | Unset
        if isinstance(_expected_instance_ids, Unset):
            expected_instance_ids = UNSET
        else:
            expected_instance_ids = TaskToolRetirementRequestExpectedInstanceIds.from_dict(_expected_instance_ids)

        _expected_mcp_access_policy = d.pop("expected_mcp_access_policy", UNSET)
        expected_mcp_access_policy: MCPAccessPolicyExpectation | Unset
        if isinstance(_expected_mcp_access_policy, Unset):
            expected_mcp_access_policy = UNSET
        else:
            expected_mcp_access_policy = MCPAccessPolicyExpectation.from_dict(_expected_mcp_access_policy)

        task_tool_retirement_request = cls(
            action=action,
            qualification=qualification,
            qualification_sha256=qualification_sha256,
            claim_token=claim_token,
            bindings_sha256=bindings_sha256,
            evidence_sha256=evidence_sha256,
            checked_at=checked_at,
            shutdown_timeout_ms=shutdown_timeout_ms,
            expected_instance_ids=expected_instance_ids,
            expected_mcp_access_policy=expected_mcp_access_policy,
        )

        return task_tool_retirement_request
