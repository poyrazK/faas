from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.task_tool_release_execution_completed_stages_item import (
    TaskToolReleaseExecutionCompletedStagesItem,
    check_task_tool_release_execution_completed_stages_item,
)
from ..models.task_tool_release_execution_fence_version import (
    TaskToolReleaseExecutionFenceVersion,
    check_task_tool_release_execution_fence_version,
)
from ..models.task_tool_release_execution_stage import (
    TaskToolReleaseExecutionStage,
    check_task_tool_release_execution_stage,
)
from ..models.task_tool_release_execution_state import (
    TaskToolReleaseExecutionState,
    check_task_tool_release_execution_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_recovery_handover import TaskToolRecoveryHandover
    from ..models.task_tool_release_receipt import TaskToolReleaseReceipt


T = TypeVar("T", bound="TaskToolReleaseExecution")


@_attrs_define
class TaskToolReleaseExecution:
    """Single-use owner authorization bound to a private local binding digest and an immutable operation. Existing claims
    are never automatically taken over.

    """

    operation_id: str
    bindings_sha256: str
    authorizer_key_id: UUID
    state: TaskToolReleaseExecutionState
    authorized_at: datetime.datetime
    claim_sha256: str | Unset = UNSET
    stage: TaskToolReleaseExecutionStage | Unset = UNSET
    receipt: TaskToolReleaseReceipt | Unset = UNSET
    """Monotonic checkpoint from the authenticated local execution claim holder. Deployment and worker identities
    require independent fresh native observation during recovery; this metadata grants no action authority."""
    completed_stages: list[TaskToolReleaseExecutionCompletedStagesItem] | Unset = UNSET
    fence_version: TaskToolReleaseExecutionFenceVersion | Unset = UNSET
    executor_exited_at: datetime.datetime | Unset = UNSET
    recovery: TaskToolRecoveryHandover | Unset = UNSET
    """Permanent executor fence and separate owner recovery authority. Settlement requires an exit acknowledgement
    and the native mutation barrier. Restore and quarantine have separate authority and retained results."""

    def to_dict(self) -> dict[str, Any]:
        operation_id = self.operation_id

        bindings_sha256 = self.bindings_sha256

        authorizer_key_id = str(self.authorizer_key_id)

        state: str = self.state

        authorized_at = self.authorized_at.isoformat()

        claim_sha256 = self.claim_sha256

        stage: str | Unset = UNSET
        if not isinstance(self.stage, Unset):
            stage = self.stage

        receipt: dict[str, Any] | Unset = UNSET
        if not isinstance(self.receipt, Unset):
            receipt = self.receipt.to_dict()

        completed_stages: list[str] | Unset = UNSET
        if not isinstance(self.completed_stages, Unset):
            completed_stages = []
            for completed_stages_item_data in self.completed_stages:
                completed_stages_item: str = completed_stages_item_data
                completed_stages.append(completed_stages_item)

        fence_version: int | Unset = UNSET
        if not isinstance(self.fence_version, Unset):
            fence_version = self.fence_version

        executor_exited_at: str | Unset = UNSET
        if not isinstance(self.executor_exited_at, Unset):
            executor_exited_at = self.executor_exited_at.isoformat()

        recovery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery, Unset):
            recovery = self.recovery.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "bindings_sha256": bindings_sha256,
                "authorizer_key_id": authorizer_key_id,
                "state": state,
                "authorized_at": authorized_at,
            }
        )
        if claim_sha256 is not UNSET:
            field_dict["claim_sha256"] = claim_sha256
        if stage is not UNSET:
            field_dict["stage"] = stage
        if receipt is not UNSET:
            field_dict["receipt"] = receipt
        if completed_stages is not UNSET:
            field_dict["completed_stages"] = completed_stages
        if fence_version is not UNSET:
            field_dict["fence_version"] = fence_version
        if executor_exited_at is not UNSET:
            field_dict["executor_exited_at"] = executor_exited_at
        if recovery is not UNSET:
            field_dict["recovery"] = recovery

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_recovery_handover import TaskToolRecoveryHandover
        from ..models.task_tool_release_receipt import TaskToolReleaseReceipt

        d = dict(src_dict)
        operation_id = d.pop("operation_id")

        bindings_sha256 = d.pop("bindings_sha256")

        authorizer_key_id = UUID(d.pop("authorizer_key_id"))

        state = check_task_tool_release_execution_state(d.pop("state"))

        authorized_at = datetime.datetime.fromisoformat(d.pop("authorized_at"))

        claim_sha256 = d.pop("claim_sha256", UNSET)

        _stage = d.pop("stage", UNSET)
        stage: TaskToolReleaseExecutionStage | Unset
        if isinstance(_stage, Unset):
            stage = UNSET
        else:
            stage = check_task_tool_release_execution_stage(_stage)

        _receipt = d.pop("receipt", UNSET)
        receipt: TaskToolReleaseReceipt | Unset
        if isinstance(_receipt, Unset):
            receipt = UNSET
        else:
            receipt = TaskToolReleaseReceipt.from_dict(_receipt)

        _completed_stages = d.pop("completed_stages", UNSET)
        completed_stages: list[TaskToolReleaseExecutionCompletedStagesItem] | Unset = UNSET
        if _completed_stages is not UNSET:
            completed_stages = []
            for completed_stages_item_data in _completed_stages:
                completed_stages_item = check_task_tool_release_execution_completed_stages_item(
                    completed_stages_item_data
                )

                completed_stages.append(completed_stages_item)

        _fence_version = d.pop("fence_version", UNSET)
        fence_version: TaskToolReleaseExecutionFenceVersion | Unset
        if isinstance(_fence_version, Unset):
            fence_version = UNSET
        else:
            fence_version = check_task_tool_release_execution_fence_version(_fence_version)

        _executor_exited_at = d.pop("executor_exited_at", UNSET)
        executor_exited_at: datetime.datetime | Unset
        if isinstance(_executor_exited_at, Unset):
            executor_exited_at = UNSET
        else:
            executor_exited_at = datetime.datetime.fromisoformat(_executor_exited_at)

        _recovery = d.pop("recovery", UNSET)
        recovery: TaskToolRecoveryHandover | Unset
        if isinstance(_recovery, Unset):
            recovery = UNSET
        else:
            recovery = TaskToolRecoveryHandover.from_dict(_recovery)

        task_tool_release_execution = cls(
            operation_id=operation_id,
            bindings_sha256=bindings_sha256,
            authorizer_key_id=authorizer_key_id,
            state=state,
            authorized_at=authorized_at,
            claim_sha256=claim_sha256,
            stage=stage,
            receipt=receipt,
            completed_stages=completed_stages,
            fence_version=fence_version,
            executor_exited_at=executor_exited_at,
            recovery=recovery,
        )

        return task_tool_release_execution
