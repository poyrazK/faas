from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.task_tool_retirement_decision import TaskToolRetirementDecision, check_task_tool_retirement_decision
from ..models.task_tool_retirement_state import TaskToolRetirementState, check_task_tool_retirement_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_recovery_qualification import TaskToolRecoveryQualification
    from ..models.task_tool_retirement_result import TaskToolRetirementResult


T = TypeVar("T", bound="TaskToolRetirement")


@_attrs_define
class TaskToolRetirement:
    """Separate fixed owner approval for atomic native conditional parking. Pending means a park request committed; retired
    also requires native drain and absent or expired Task registrations. Restore and quarantine approvals do not
    authorize retirement.

    """

    qualification: TaskToolRecoveryQualification
    """Owner attestation of fresh local checks, exact recovery target and a locked native snapshot. The fixed
    deadline is observed_at plus 60 seconds, independently of the original release approval. Qualifying or approving
    never renews it."""
    state: TaskToolRetirementState
    decision: TaskToolRetirementDecision | Unset = UNSET
    authorizer_key_id: UUID | Unset = UNSET
    decided_at: datetime.datetime | Unset = UNSET
    park_transition_id: UUID | Unset = UNSET
    park_requested_at: datetime.datetime | Unset = UNSET
    result: TaskToolRetirementResult | Unset = UNSET
    """Historical observation of the exact native park transition, drained instances and absent or expired
    candidate Task registrations. No active Task cancellation or current service-health certification is implied.
   """

    def to_dict(self) -> dict[str, Any]:
        qualification = self.qualification.to_dict()

        state: str = self.state

        decision: str | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision

        authorizer_key_id: str | Unset = UNSET
        if not isinstance(self.authorizer_key_id, Unset):
            authorizer_key_id = str(self.authorizer_key_id)

        decided_at: str | Unset = UNSET
        if not isinstance(self.decided_at, Unset):
            decided_at = self.decided_at.isoformat()

        park_transition_id: str | Unset = UNSET
        if not isinstance(self.park_transition_id, Unset):
            park_transition_id = str(self.park_transition_id)

        park_requested_at: str | Unset = UNSET
        if not isinstance(self.park_requested_at, Unset):
            park_requested_at = self.park_requested_at.isoformat()

        result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "qualification": qualification,
                "state": state,
            }
        )
        if decision is not UNSET:
            field_dict["decision"] = decision
        if authorizer_key_id is not UNSET:
            field_dict["authorizer_key_id"] = authorizer_key_id
        if decided_at is not UNSET:
            field_dict["decided_at"] = decided_at
        if park_transition_id is not UNSET:
            field_dict["park_transition_id"] = park_transition_id
        if park_requested_at is not UNSET:
            field_dict["park_requested_at"] = park_requested_at
        if result is not UNSET:
            field_dict["result"] = result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_recovery_qualification import TaskToolRecoveryQualification
        from ..models.task_tool_retirement_result import TaskToolRetirementResult

        d = dict(src_dict)
        qualification = TaskToolRecoveryQualification.from_dict(d.pop("qualification"))

        state = check_task_tool_retirement_state(d.pop("state"))

        _decision = d.pop("decision", UNSET)
        decision: TaskToolRetirementDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = check_task_tool_retirement_decision(_decision)

        _authorizer_key_id = d.pop("authorizer_key_id", UNSET)
        authorizer_key_id: UUID | Unset
        if isinstance(_authorizer_key_id, Unset):
            authorizer_key_id = UNSET
        else:
            authorizer_key_id = UUID(_authorizer_key_id)

        _decided_at = d.pop("decided_at", UNSET)
        decided_at: datetime.datetime | Unset
        if isinstance(_decided_at, Unset):
            decided_at = UNSET
        else:
            decided_at = datetime.datetime.fromisoformat(_decided_at)

        _park_transition_id = d.pop("park_transition_id", UNSET)
        park_transition_id: UUID | Unset
        if isinstance(_park_transition_id, Unset):
            park_transition_id = UNSET
        else:
            park_transition_id = UUID(_park_transition_id)

        _park_requested_at = d.pop("park_requested_at", UNSET)
        park_requested_at: datetime.datetime | Unset
        if isinstance(_park_requested_at, Unset):
            park_requested_at = UNSET
        else:
            park_requested_at = datetime.datetime.fromisoformat(_park_requested_at)

        _result = d.pop("result", UNSET)
        result: TaskToolRetirementResult | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = TaskToolRetirementResult.from_dict(_result)

        task_tool_retirement = cls(
            qualification=qualification,
            state=state,
            decision=decision,
            authorizer_key_id=authorizer_key_id,
            decided_at=decided_at,
            park_transition_id=park_transition_id,
            park_requested_at=park_requested_at,
            result=result,
        )

        return task_tool_retirement
