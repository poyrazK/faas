from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.task_tool_recovery_result_action import (
    TaskToolRecoveryResultAction,
    check_task_tool_recovery_result_action,
)

T = TypeVar("T", bound="TaskToolRecoveryResult")


@_attrs_define
class TaskToolRecoveryResult:
    """Durable atomic traffic-commit receipt. It binds the consumed owner approval to the exact deployments and evidence,
    and remains inspectable after deadline expiry or revocation. It never authorizes another write or certifies current
    health.

    """

    intent_id: str
    operation_id: str
    handover_id: str
    qualification_sha256: str
    revision: int
    action: TaskToolRecoveryResultAction
    target_deployment_id: UUID
    previous_serving_deployment_id: UUID
    evidence_sha256: str
    checked_at: datetime.datetime
    committed_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        intent_id = self.intent_id

        operation_id = self.operation_id

        handover_id = self.handover_id

        qualification_sha256 = self.qualification_sha256

        revision = self.revision

        action: str = self.action

        target_deployment_id = str(self.target_deployment_id)

        previous_serving_deployment_id = str(self.previous_serving_deployment_id)

        evidence_sha256 = self.evidence_sha256

        checked_at = self.checked_at.isoformat()

        committed_at = self.committed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "intent_id": intent_id,
                "operation_id": operation_id,
                "handover_id": handover_id,
                "qualification_sha256": qualification_sha256,
                "revision": revision,
                "action": action,
                "target_deployment_id": target_deployment_id,
                "previous_serving_deployment_id": previous_serving_deployment_id,
                "evidence_sha256": evidence_sha256,
                "checked_at": checked_at,
                "committed_at": committed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        intent_id = d.pop("intent_id")

        operation_id = d.pop("operation_id")

        handover_id = d.pop("handover_id")

        qualification_sha256 = d.pop("qualification_sha256")

        revision = d.pop("revision")

        action = check_task_tool_recovery_result_action(d.pop("action"))

        target_deployment_id = UUID(d.pop("target_deployment_id"))

        previous_serving_deployment_id = UUID(d.pop("previous_serving_deployment_id"))

        evidence_sha256 = d.pop("evidence_sha256")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        committed_at = datetime.datetime.fromisoformat(d.pop("committed_at"))

        task_tool_recovery_result = cls(
            intent_id=intent_id,
            operation_id=operation_id,
            handover_id=handover_id,
            qualification_sha256=qualification_sha256,
            revision=revision,
            action=action,
            target_deployment_id=target_deployment_id,
            previous_serving_deployment_id=previous_serving_deployment_id,
            evidence_sha256=evidence_sha256,
            checked_at=checked_at,
            committed_at=committed_at,
        )

        return task_tool_recovery_result
