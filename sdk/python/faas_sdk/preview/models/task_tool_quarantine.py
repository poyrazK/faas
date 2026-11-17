from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.task_tool_quarantine_decision import TaskToolQuarantineDecision, check_task_tool_quarantine_decision
from ..models.task_tool_quarantine_state import TaskToolQuarantineState, check_task_tool_quarantine_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_quarantine_result import TaskToolQuarantineResult
    from ..models.task_tool_recovery_qualification import TaskToolRecoveryQualification


T = TypeVar("T", bound="TaskToolQuarantine")


@_attrs_define
class TaskToolQuarantine:
    """Separate fixed owner approval and one admitted candidate claim-fence attempt. Applying is uncertain until read-only
    Task-database evidence is observed. It cannot renew or reuse restore authority.

    """

    qualification: TaskToolRecoveryQualification
    """Owner attestation of fresh local checks, exact recovery target and a locked native snapshot. The fixed
    deadline is observed_at plus 60 seconds, independently of the original release approval. Qualifying or approving
    never renews it."""
    state: TaskToolQuarantineState
    decision: TaskToolQuarantineDecision | Unset = UNSET
    authorizer_key_id: UUID | Unset = UNSET
    decided_at: datetime.datetime | Unset = UNSET
    attempt_sha256: str | Unset = UNSET
    claimed_at: datetime.datetime | Unset = UNSET
    result: TaskToolQuarantineResult | Unset = UNSET
    """Historical observation that the exact recorded candidate worker IDs were fenced from new claims. No cross-
    database atomic commit, active-lease cancellation, worker shutdown or current-health certification is implied.
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

        attempt_sha256 = self.attempt_sha256

        claimed_at: str | Unset = UNSET
        if not isinstance(self.claimed_at, Unset):
            claimed_at = self.claimed_at.isoformat()

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
        if attempt_sha256 is not UNSET:
            field_dict["attempt_sha256"] = attempt_sha256
        if claimed_at is not UNSET:
            field_dict["claimed_at"] = claimed_at
        if result is not UNSET:
            field_dict["result"] = result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_quarantine_result import TaskToolQuarantineResult
        from ..models.task_tool_recovery_qualification import TaskToolRecoveryQualification

        d = dict(src_dict)
        qualification = TaskToolRecoveryQualification.from_dict(d.pop("qualification"))

        state = check_task_tool_quarantine_state(d.pop("state"))

        _decision = d.pop("decision", UNSET)
        decision: TaskToolQuarantineDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = check_task_tool_quarantine_decision(_decision)

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

        attempt_sha256 = d.pop("attempt_sha256", UNSET)

        _claimed_at = d.pop("claimed_at", UNSET)
        claimed_at: datetime.datetime | Unset
        if isinstance(_claimed_at, Unset):
            claimed_at = UNSET
        else:
            claimed_at = datetime.datetime.fromisoformat(_claimed_at)

        _result = d.pop("result", UNSET)
        result: TaskToolQuarantineResult | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = TaskToolQuarantineResult.from_dict(_result)

        task_tool_quarantine = cls(
            qualification=qualification,
            state=state,
            decision=decision,
            authorizer_key_id=authorizer_key_id,
            decided_at=decided_at,
            attempt_sha256=attempt_sha256,
            claimed_at=claimed_at,
            result=result,
        )

        return task_tool_quarantine
