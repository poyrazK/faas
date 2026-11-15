from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.task_tool_release_execution_request_action import (
    TaskToolReleaseExecutionRequestAction,
    check_task_tool_release_execution_request_action,
)
from ..models.task_tool_release_execution_request_stage import (
    TaskToolReleaseExecutionRequestStage,
    check_task_tool_release_execution_request_stage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_release_receipt import TaskToolReleaseReceipt


T = TypeVar("T", bound="TaskToolReleaseExecutionRequest")


@_attrs_define
class TaskToolReleaseExecutionRequest:
    """Exact local execution identity and bounded transition. Private claim material is submitted by the trusted CLI and is
    never returned in API receipts.

    """

    action: TaskToolReleaseExecutionRequestAction
    operation_id: str
    bindings_sha256: str
    claim_token: str | Unset = UNSET
    stage: TaskToolReleaseExecutionRequestStage | Unset = UNSET
    receipt: TaskToolReleaseReceipt | Unset = UNSET
    """Monotonic checkpoint from the authenticated local execution claim holder. Deployment and worker identities
    require independent fresh native observation during recovery; this metadata grants no action authority."""

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        operation_id = self.operation_id

        bindings_sha256 = self.bindings_sha256

        claim_token = self.claim_token

        stage: str | Unset = UNSET
        if not isinstance(self.stage, Unset):
            stage = self.stage

        receipt: dict[str, Any] | Unset = UNSET
        if not isinstance(self.receipt, Unset):
            receipt = self.receipt.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "action": action,
                "operation_id": operation_id,
                "bindings_sha256": bindings_sha256,
            }
        )
        if claim_token is not UNSET:
            field_dict["claim_token"] = claim_token
        if stage is not UNSET:
            field_dict["stage"] = stage
        if receipt is not UNSET:
            field_dict["receipt"] = receipt

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_release_receipt import TaskToolReleaseReceipt

        d = dict(src_dict)
        action = check_task_tool_release_execution_request_action(d.pop("action"))

        operation_id = d.pop("operation_id")

        bindings_sha256 = d.pop("bindings_sha256")

        claim_token = d.pop("claim_token", UNSET)

        _stage = d.pop("stage", UNSET)
        stage: TaskToolReleaseExecutionRequestStage | Unset
        if isinstance(_stage, Unset):
            stage = UNSET
        else:
            stage = check_task_tool_release_execution_request_stage(_stage)

        _receipt = d.pop("receipt", UNSET)
        receipt: TaskToolReleaseReceipt | Unset
        if isinstance(_receipt, Unset):
            receipt = UNSET
        else:
            receipt = TaskToolReleaseReceipt.from_dict(_receipt)

        task_tool_release_execution_request = cls(
            action=action,
            operation_id=operation_id,
            bindings_sha256=bindings_sha256,
            claim_token=claim_token,
            stage=stage,
            receipt=receipt,
        )

        return task_tool_release_execution_request
