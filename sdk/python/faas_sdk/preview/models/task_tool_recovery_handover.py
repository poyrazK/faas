from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.task_tool_recovery_handover_decision import (
    TaskToolRecoveryHandoverDecision,
    check_task_tool_recovery_handover_decision,
)
from ..models.task_tool_recovery_handover_state import (
    TaskToolRecoveryHandoverState,
    check_task_tool_recovery_handover_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_quarantine import TaskToolQuarantine
    from ..models.task_tool_recovery_qualification import TaskToolRecoveryQualification
    from ..models.task_tool_recovery_result import TaskToolRecoveryResult
    from ..models.task_tool_retirement import TaskToolRetirement


T = TypeVar("T", bound="TaskToolRecoveryHandover")


@_attrs_define
class TaskToolRecoveryHandover:
    """Permanent executor fence and separate owner recovery authority. Settlement requires an exit acknowledgement and the
    native mutation barrier. Restore and quarantine have separate authority and retained results.

    """

    id: str
    operation_id: str
    revision: int
    state: TaskToolRecoveryHandoverState
    requested_at: datetime.datetime
    retirement: TaskToolRetirement | Unset = UNSET
    """Separate fixed owner approval for atomic native conditional parking. Pending means a park request committed;
    retired also requires native drain and absent or expired Task registrations. Restore and quarantine approvals do
    not authorize retirement."""
    quarantine: TaskToolQuarantine | Unset = UNSET
    """Separate fixed owner approval and one admitted candidate claim-fence attempt. Applying is uncertain until
    read-only Task-database evidence is observed. It cannot renew or reuse restore authority."""
    result: TaskToolRecoveryResult | Unset = UNSET
    """Durable atomic traffic-commit receipt. It binds the consumed owner approval to the exact deployments and
    evidence, and remains inspectable after deadline expiry or revocation. It never authorizes another write or
    certifies current health."""
    settled_at: datetime.datetime | Unset = UNSET
    qualification: TaskToolRecoveryQualification | Unset = UNSET
    """Owner attestation of fresh local checks, exact recovery target and a locked native snapshot. The fixed
    deadline is observed_at plus 60 seconds, independently of the original release approval. Qualifying or approving
    never renews it."""
    decision: TaskToolRecoveryHandoverDecision | Unset = UNSET
    authorizer_key_id: UUID | Unset = UNSET
    decided_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        operation_id = self.operation_id

        revision = self.revision

        state: str = self.state

        requested_at = self.requested_at.isoformat()

        retirement: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retirement, Unset):
            retirement = self.retirement.to_dict()

        quarantine: dict[str, Any] | Unset = UNSET
        if not isinstance(self.quarantine, Unset):
            quarantine = self.quarantine.to_dict()

        result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result.to_dict()

        settled_at: str | Unset = UNSET
        if not isinstance(self.settled_at, Unset):
            settled_at = self.settled_at.isoformat()

        qualification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.qualification, Unset):
            qualification = self.qualification.to_dict()

        decision: str | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision

        authorizer_key_id: str | Unset = UNSET
        if not isinstance(self.authorizer_key_id, Unset):
            authorizer_key_id = str(self.authorizer_key_id)

        decided_at: str | Unset = UNSET
        if not isinstance(self.decided_at, Unset):
            decided_at = self.decided_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "operation_id": operation_id,
                "revision": revision,
                "state": state,
                "requested_at": requested_at,
            }
        )
        if retirement is not UNSET:
            field_dict["retirement"] = retirement
        if quarantine is not UNSET:
            field_dict["quarantine"] = quarantine
        if result is not UNSET:
            field_dict["result"] = result
        if settled_at is not UNSET:
            field_dict["settled_at"] = settled_at
        if qualification is not UNSET:
            field_dict["qualification"] = qualification
        if decision is not UNSET:
            field_dict["decision"] = decision
        if authorizer_key_id is not UNSET:
            field_dict["authorizer_key_id"] = authorizer_key_id
        if decided_at is not UNSET:
            field_dict["decided_at"] = decided_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_quarantine import TaskToolQuarantine
        from ..models.task_tool_recovery_qualification import TaskToolRecoveryQualification
        from ..models.task_tool_recovery_result import TaskToolRecoveryResult
        from ..models.task_tool_retirement import TaskToolRetirement

        d = dict(src_dict)
        id = d.pop("id")

        operation_id = d.pop("operation_id")

        revision = d.pop("revision")

        state = check_task_tool_recovery_handover_state(d.pop("state"))

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        _retirement = d.pop("retirement", UNSET)
        retirement: TaskToolRetirement | Unset
        if isinstance(_retirement, Unset):
            retirement = UNSET
        else:
            retirement = TaskToolRetirement.from_dict(_retirement)

        _quarantine = d.pop("quarantine", UNSET)
        quarantine: TaskToolQuarantine | Unset
        if isinstance(_quarantine, Unset):
            quarantine = UNSET
        else:
            quarantine = TaskToolQuarantine.from_dict(_quarantine)

        _result = d.pop("result", UNSET)
        result: TaskToolRecoveryResult | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = TaskToolRecoveryResult.from_dict(_result)

        _settled_at = d.pop("settled_at", UNSET)
        settled_at: datetime.datetime | Unset
        if isinstance(_settled_at, Unset):
            settled_at = UNSET
        else:
            settled_at = datetime.datetime.fromisoformat(_settled_at)

        _qualification = d.pop("qualification", UNSET)
        qualification: TaskToolRecoveryQualification | Unset
        if isinstance(_qualification, Unset):
            qualification = UNSET
        else:
            qualification = TaskToolRecoveryQualification.from_dict(_qualification)

        _decision = d.pop("decision", UNSET)
        decision: TaskToolRecoveryHandoverDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = check_task_tool_recovery_handover_decision(_decision)

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

        task_tool_recovery_handover = cls(
            id=id,
            operation_id=operation_id,
            revision=revision,
            state=state,
            requested_at=requested_at,
            retirement=retirement,
            quarantine=quarantine,
            result=result,
            settled_at=settled_at,
            qualification=qualification,
            decision=decision,
            authorizer_key_id=authorizer_key_id,
            decided_at=decided_at,
        )

        return task_tool_recovery_handover
