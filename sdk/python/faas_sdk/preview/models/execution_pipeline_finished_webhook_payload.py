from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_pipeline_finished_webhook_payload_outcome import (
    ExecutionPipelineFinishedWebhookPayloadOutcome,
    check_execution_pipeline_finished_webhook_payload_outcome,
)

T = TypeVar("T", bound="ExecutionPipelineFinishedWebhookPayload")


@_attrs_define
class ExecutionPipelineFinishedWebhookPayload:
    """Account webhook metadata for execution.pipeline.finished. Receivers fetch the authorized workflow receipt for the
    complete manifest. Uses existing signed delivery, retries, dead-letter and redrive.

    """

    event_id: UUID
    workflow_id: str
    outcome: ExecutionPipelineFinishedWebhookPayloadOutcome
    finished_at: datetime.datetime
    receipt_url: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        workflow_id = self.workflow_id

        outcome: str = self.outcome

        finished_at = self.finished_at.isoformat()

        receipt_url = self.receipt_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "workflow_id": workflow_id,
                "outcome": outcome,
                "finished_at": finished_at,
                "receipt_url": receipt_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        workflow_id = d.pop("workflow_id")

        outcome = check_execution_pipeline_finished_webhook_payload_outcome(d.pop("outcome"))

        finished_at = datetime.datetime.fromisoformat(d.pop("finished_at"))

        receipt_url = d.pop("receipt_url")

        execution_pipeline_finished_webhook_payload = cls(
            event_id=event_id,
            workflow_id=workflow_id,
            outcome=outcome,
            finished_at=finished_at,
            receipt_url=receipt_url,
        )

        execution_pipeline_finished_webhook_payload.additional_properties = d
        return execution_pipeline_finished_webhook_payload

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
