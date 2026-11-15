from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.task_tool_release_execution_stage import (
    TaskToolReleaseExecutionStage,
    check_task_tool_release_execution_stage,
)
from ..models.task_tool_release_execution_state import (
    TaskToolReleaseExecutionState,
    check_task_tool_release_execution_state,
)
from ..types import UNSET, Unset

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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
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

        task_tool_release_execution = cls(
            operation_id=operation_id,
            bindings_sha256=bindings_sha256,
            authorizer_key_id=authorizer_key_id,
            state=state,
            authorized_at=authorized_at,
            claim_sha256=claim_sha256,
            stage=stage,
        )

        return task_tool_release_execution
