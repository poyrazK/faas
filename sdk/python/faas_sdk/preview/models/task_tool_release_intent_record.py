from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.task_tool_release_intent_record_status import (
    TaskToolReleaseIntentRecordStatus,
    check_task_tool_release_intent_record_status,
)
from ..models.task_tool_release_intent_record_version import (
    TaskToolReleaseIntentRecordVersion,
    check_task_tool_release_intent_record_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_release_execution import TaskToolReleaseExecution
    from ..models.task_tool_release_intent import TaskToolReleaseIntent


T = TypeVar("T", bound="TaskToolReleaseIntentRecord")


@_attrs_define
class TaskToolReleaseIntentRecord:
    """Immutable source and native app review with optional separate owner authorization for a single local Task adapter
    execution. Root readiness flags never grant authority.

    """

    version: TaskToolReleaseIntentRecordVersion
    id: str
    account_id: UUID
    requester_key_id: UUID
    intent_sha256: str
    intent: TaskToolReleaseIntent
    """Immutable review metadata bounded to 64 KiB. Required setup remains unverified. No source paths, executable
    arguments, database URLs, credentials or credential references."""
    status: TaskToolReleaseIntentRecordStatus
    created_at: datetime.datetime
    expires_at: datetime.datetime
    bindings_verified: bool
    ready_to_apply: bool
    decided_at: datetime.datetime | Unset = UNSET
    execution: TaskToolReleaseExecution | Unset = UNSET
    """Single-use owner authorization bound to a private local binding digest and an immutable operation. Existing
    claims are never automatically taken over."""

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        id = self.id

        account_id = str(self.account_id)

        requester_key_id = str(self.requester_key_id)

        intent_sha256 = self.intent_sha256

        intent = self.intent.to_dict()

        status: str = self.status

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        bindings_verified = self.bindings_verified

        ready_to_apply = self.ready_to_apply

        decided_at: str | Unset = UNSET
        if not isinstance(self.decided_at, Unset):
            decided_at = self.decided_at.isoformat()

        execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.execution, Unset):
            execution = self.execution.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "id": id,
                "account_id": account_id,
                "requester_key_id": requester_key_id,
                "intent_sha256": intent_sha256,
                "intent": intent,
                "status": status,
                "created_at": created_at,
                "expires_at": expires_at,
                "bindings_verified": bindings_verified,
                "ready_to_apply": ready_to_apply,
            }
        )
        if decided_at is not UNSET:
            field_dict["decided_at"] = decided_at
        if execution is not UNSET:
            field_dict["execution"] = execution

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_release_execution import TaskToolReleaseExecution
        from ..models.task_tool_release_intent import TaskToolReleaseIntent

        d = dict(src_dict)
        version = check_task_tool_release_intent_record_version(d.pop("version"))

        id = d.pop("id")

        account_id = UUID(d.pop("account_id"))

        requester_key_id = UUID(d.pop("requester_key_id"))

        intent_sha256 = d.pop("intent_sha256")

        intent = TaskToolReleaseIntent.from_dict(d.pop("intent"))

        status = check_task_tool_release_intent_record_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        bindings_verified = d.pop("bindings_verified")

        ready_to_apply = d.pop("ready_to_apply")

        _decided_at = d.pop("decided_at", UNSET)
        decided_at: datetime.datetime | Unset
        if isinstance(_decided_at, Unset):
            decided_at = UNSET
        else:
            decided_at = datetime.datetime.fromisoformat(_decided_at)

        _execution = d.pop("execution", UNSET)
        execution: TaskToolReleaseExecution | Unset
        if isinstance(_execution, Unset):
            execution = UNSET
        else:
            execution = TaskToolReleaseExecution.from_dict(_execution)

        task_tool_release_intent_record = cls(
            version=version,
            id=id,
            account_id=account_id,
            requester_key_id=requester_key_id,
            intent_sha256=intent_sha256,
            intent=intent,
            status=status,
            created_at=created_at,
            expires_at=expires_at,
            bindings_verified=bindings_verified,
            ready_to_apply=ready_to_apply,
            decided_at=decided_at,
            execution=execution,
        )

        return task_tool_release_intent_record
