from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.task_tool_release_intent_decision_decision import (
    TaskToolReleaseIntentDecisionDecision,
    check_task_tool_release_intent_decision_decision,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_quarantine_request import TaskToolQuarantineRequest
    from ..models.task_tool_recovery_request import TaskToolRecoveryRequest
    from ..models.task_tool_release_execution_request import TaskToolReleaseExecutionRequest


T = TypeVar("T", bound="TaskToolReleaseIntentDecision")


@_attrs_define
class TaskToolReleaseIntentDecision:
    """Owner verdict bound to an immutable Task intent digest and its retained source fingerprint."""

    intent_sha256: str
    release_fingerprint: str
    decision: TaskToolReleaseIntentDecisionDecision
    quarantine: TaskToolQuarantineRequest | Unset = UNSET
    """Private trusted CLI qualification or attempt metadata on existing journal routes. Agent tools accept public
    identities only. A claim is never automatically retried; observe records read-only fence evidence after an
    admitted attempt."""
    execution: TaskToolReleaseExecutionRequest | Unset = UNSET
    """Exact local execution identity and bounded transition. Private claim material is submitted by the trusted
    CLI and is never returned in API receipts."""
    recovery: TaskToolRecoveryRequest | Unset = UNSET
    """Owner-only handover or qualification verdict on the existing decision route. The authenticated native
    handler supplies the owner identity; agent inputs cannot supply it."""

    def to_dict(self) -> dict[str, Any]:
        intent_sha256 = self.intent_sha256

        release_fingerprint = self.release_fingerprint

        decision: str = self.decision

        quarantine: dict[str, Any] | Unset = UNSET
        if not isinstance(self.quarantine, Unset):
            quarantine = self.quarantine.to_dict()

        execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.execution, Unset):
            execution = self.execution.to_dict()

        recovery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery, Unset):
            recovery = self.recovery.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "intent_sha256": intent_sha256,
                "release_fingerprint": release_fingerprint,
                "decision": decision,
            }
        )
        if quarantine is not UNSET:
            field_dict["quarantine"] = quarantine
        if execution is not UNSET:
            field_dict["execution"] = execution
        if recovery is not UNSET:
            field_dict["recovery"] = recovery

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_quarantine_request import TaskToolQuarantineRequest
        from ..models.task_tool_recovery_request import TaskToolRecoveryRequest
        from ..models.task_tool_release_execution_request import TaskToolReleaseExecutionRequest

        d = dict(src_dict)
        intent_sha256 = d.pop("intent_sha256")

        release_fingerprint = d.pop("release_fingerprint")

        decision = check_task_tool_release_intent_decision_decision(d.pop("decision"))

        _quarantine = d.pop("quarantine", UNSET)
        quarantine: TaskToolQuarantineRequest | Unset
        if isinstance(_quarantine, Unset):
            quarantine = UNSET
        else:
            quarantine = TaskToolQuarantineRequest.from_dict(_quarantine)

        _execution = d.pop("execution", UNSET)
        execution: TaskToolReleaseExecutionRequest | Unset
        if isinstance(_execution, Unset):
            execution = UNSET
        else:
            execution = TaskToolReleaseExecutionRequest.from_dict(_execution)

        _recovery = d.pop("recovery", UNSET)
        recovery: TaskToolRecoveryRequest | Unset
        if isinstance(_recovery, Unset):
            recovery = UNSET
        else:
            recovery = TaskToolRecoveryRequest.from_dict(_recovery)

        task_tool_release_intent_decision = cls(
            intent_sha256=intent_sha256,
            release_fingerprint=release_fingerprint,
            decision=decision,
            quarantine=quarantine,
            execution=execution,
            recovery=recovery,
        )

        return task_tool_release_intent_decision
