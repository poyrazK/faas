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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
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
        )

        return task_tool_release_execution
