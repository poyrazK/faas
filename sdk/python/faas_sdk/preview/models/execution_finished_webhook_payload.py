from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionFinishedWebhookPayload")


@_attrs_define
class ExecutionFinishedWebhookPayload:
    """Account webhook payload emitted when a disposable Run reaches a terminal status."""

    event_id: str
    execution_id: str
    status: str
    runtime: str
    finished_at: str
    receipt_url: str
    environment_id: str | Unset = UNSET
    workflow_id: str | Unset = UNSET
    result_validation_status: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        execution_id = self.execution_id

        status = self.status

        runtime = self.runtime

        finished_at = self.finished_at

        receipt_url = self.receipt_url

        environment_id = self.environment_id

        workflow_id = self.workflow_id

        result_validation_status = self.result_validation_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "execution_id": execution_id,
                "status": status,
                "runtime": runtime,
                "finished_at": finished_at,
                "receipt_url": receipt_url,
            }
        )
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if workflow_id is not UNSET:
            field_dict["workflow_id"] = workflow_id
        if result_validation_status is not UNSET:
            field_dict["result_validation_status"] = result_validation_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = d.pop("event_id")

        execution_id = d.pop("execution_id")

        status = d.pop("status")

        runtime = d.pop("runtime")

        finished_at = d.pop("finished_at")

        receipt_url = d.pop("receipt_url")

        environment_id = d.pop("environment_id", UNSET)

        workflow_id = d.pop("workflow_id", UNSET)

        result_validation_status = d.pop("result_validation_status", UNSET)

        execution_finished_webhook_payload = cls(
            event_id=event_id,
            execution_id=execution_id,
            status=status,
            runtime=runtime,
            finished_at=finished_at,
            receipt_url=receipt_url,
            environment_id=environment_id,
            workflow_id=workflow_id,
            result_validation_status=result_validation_status,
        )

        execution_finished_webhook_payload.additional_properties = d
        return execution_finished_webhook_payload

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
