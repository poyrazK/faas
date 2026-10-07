from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.workflow_finished_webhook_payload_status import (
    WorkflowFinishedWebhookPayloadStatus,
    check_workflow_finished_webhook_payload_status,
)

T = TypeVar("T", bound="WorkflowFinishedWebhookPayload")


@_attrs_define
class WorkflowFinishedWebhookPayload:
    """Safe workflow run metadata delivered after a run reaches succeeded, failed, or dead. Inputs, outputs, and error text
    are omitted; fetch the run using its ID when authorized.

    """

    app_id: UUID
    run_id: UUID
    workflow_name: str
    status: WorkflowFinishedWebhookPayloadStatus
    finished_at: datetime.datetime
    resume_count: int

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        run_id = str(self.run_id)

        workflow_name = self.workflow_name

        status: str = self.status

        finished_at = self.finished_at.isoformat()

        resume_count = self.resume_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "run_id": run_id,
                "workflow_name": workflow_name,
                "status": status,
                "finished_at": finished_at,
                "resume_count": resume_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        run_id = UUID(d.pop("run_id"))

        workflow_name = d.pop("workflow_name")

        status = check_workflow_finished_webhook_payload_status(d.pop("status"))

        finished_at = datetime.datetime.fromisoformat(d.pop("finished_at"))

        resume_count = d.pop("resume_count")

        workflow_finished_webhook_payload = cls(
            app_id=app_id,
            run_id=run_id,
            workflow_name=workflow_name,
            status=status,
            finished_at=finished_at,
            resume_count=resume_count,
        )

        return workflow_finished_webhook_payload
