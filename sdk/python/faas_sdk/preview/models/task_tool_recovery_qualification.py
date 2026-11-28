from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.task_tool_recovery_qualification_action import (
    TaskToolRecoveryQualificationAction,
    check_task_tool_recovery_qualification_action,
)

if TYPE_CHECKING:
    from ..models.task_tool_release_app import TaskToolReleaseApp


T = TypeVar("T", bound="TaskToolRecoveryQualification")


@_attrs_define
class TaskToolRecoveryQualification:
    """Owner attestation of fresh local checks, exact recovery target and a locked native snapshot. The fixed deadline is
    observed_at plus 60 seconds, independently of the original release approval. Qualifying or approving never renews
    it.

    """

    handover_id: str
    operation_id: str
    revision: int
    action: TaskToolRecoveryQualificationAction
    target_deployment_id: UUID
    expected_serving_deployment_id: UUID
    bindings_sha256: str
    evidence_sha256: str
    native_apps: list[TaskToolReleaseApp]
    observed_at: datetime.datetime
    expires_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        handover_id = self.handover_id

        operation_id = self.operation_id

        revision = self.revision

        action: str = self.action

        target_deployment_id = str(self.target_deployment_id)

        expected_serving_deployment_id = str(self.expected_serving_deployment_id)

        bindings_sha256 = self.bindings_sha256

        evidence_sha256 = self.evidence_sha256

        native_apps = []
        for native_apps_item_data in self.native_apps:
            native_apps_item = native_apps_item_data.to_dict()
            native_apps.append(native_apps_item)

        observed_at = self.observed_at.isoformat()

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "handover_id": handover_id,
                "operation_id": operation_id,
                "revision": revision,
                "action": action,
                "target_deployment_id": target_deployment_id,
                "expected_serving_deployment_id": expected_serving_deployment_id,
                "bindings_sha256": bindings_sha256,
                "evidence_sha256": evidence_sha256,
                "native_apps": native_apps,
                "observed_at": observed_at,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_release_app import TaskToolReleaseApp

        d = dict(src_dict)
        handover_id = d.pop("handover_id")

        operation_id = d.pop("operation_id")

        revision = d.pop("revision")

        action = check_task_tool_recovery_qualification_action(d.pop("action"))

        target_deployment_id = UUID(d.pop("target_deployment_id"))

        expected_serving_deployment_id = UUID(d.pop("expected_serving_deployment_id"))

        bindings_sha256 = d.pop("bindings_sha256")

        evidence_sha256 = d.pop("evidence_sha256")

        native_apps = []
        _native_apps = d.pop("native_apps")
        for native_apps_item_data in _native_apps:
            native_apps_item = TaskToolReleaseApp.from_dict(native_apps_item_data)

            native_apps.append(native_apps_item)

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        task_tool_recovery_qualification = cls(
            handover_id=handover_id,
            operation_id=operation_id,
            revision=revision,
            action=action,
            target_deployment_id=target_deployment_id,
            expected_serving_deployment_id=expected_serving_deployment_id,
            bindings_sha256=bindings_sha256,
            evidence_sha256=evidence_sha256,
            native_apps=native_apps,
            observed_at=observed_at,
            expires_at=expires_at,
        )

        return task_tool_recovery_qualification
