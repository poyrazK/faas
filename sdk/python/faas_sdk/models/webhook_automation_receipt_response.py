from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.webhook_automation_receipt_response_ignored_reason import (
    WebhookAutomationReceiptResponseIgnoredReason,
    check_webhook_automation_receipt_response_ignored_reason,
)
from ..models.webhook_automation_receipt_response_routing_status import (
    WebhookAutomationReceiptResponseRoutingStatus,
    check_webhook_automation_receipt_response_routing_status,
)
from ..models.webhook_automation_receipt_response_status import (
    WebhookAutomationReceiptResponseStatus,
    check_webhook_automation_receipt_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WebhookAutomationReceiptResponse")


@_attrs_define
class WebhookAutomationReceiptResponse:
    """Durable provider event routing decision and automation admission progress."""

    receipt_id: UUID
    endpoint_id: UUID
    provider_event_id: str
    workflow_name: str
    status: WebhookAutomationReceiptResponseStatus
    duplicate: bool
    accepted_at: datetime.datetime
    event_source: str
    routing_status: WebhookAutomationReceiptResponseRoutingStatus
    ignored_reason: WebhookAutomationReceiptResponseIgnoredReason | Unset = UNSET
    run_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        receipt_id = str(self.receipt_id)

        endpoint_id = str(self.endpoint_id)

        provider_event_id = self.provider_event_id

        workflow_name = self.workflow_name

        status: str = self.status

        duplicate = self.duplicate

        accepted_at = self.accepted_at.isoformat()

        event_source = self.event_source

        routing_status: str = self.routing_status

        ignored_reason: str | Unset = UNSET
        if not isinstance(self.ignored_reason, Unset):
            ignored_reason = self.ignored_reason

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "receipt_id": receipt_id,
                "endpoint_id": endpoint_id,
                "provider_event_id": provider_event_id,
                "workflow_name": workflow_name,
                "status": status,
                "duplicate": duplicate,
                "accepted_at": accepted_at,
                "event_source": event_source,
                "routing_status": routing_status,
            }
        )
        if ignored_reason is not UNSET:
            field_dict["ignored_reason"] = ignored_reason
        if run_id is not UNSET:
            field_dict["run_id"] = run_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        receipt_id = UUID(d.pop("receipt_id"))

        endpoint_id = UUID(d.pop("endpoint_id"))

        provider_event_id = d.pop("provider_event_id")

        workflow_name = d.pop("workflow_name")

        status = check_webhook_automation_receipt_response_status(d.pop("status"))

        duplicate = d.pop("duplicate")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        event_source = d.pop("event_source")

        routing_status = check_webhook_automation_receipt_response_routing_status(d.pop("routing_status"))

        _ignored_reason = d.pop("ignored_reason", UNSET)
        ignored_reason: WebhookAutomationReceiptResponseIgnoredReason | Unset
        if isinstance(_ignored_reason, Unset):
            ignored_reason = UNSET
        else:
            ignored_reason = check_webhook_automation_receipt_response_ignored_reason(_ignored_reason)

        _run_id = d.pop("run_id", UNSET)
        run_id: UUID | Unset
        if isinstance(_run_id, Unset):
            run_id = UNSET
        else:
            run_id = UUID(_run_id)

        webhook_automation_receipt_response = cls(
            receipt_id=receipt_id,
            endpoint_id=endpoint_id,
            provider_event_id=provider_event_id,
            workflow_name=workflow_name,
            status=status,
            duplicate=duplicate,
            accepted_at=accepted_at,
            event_source=event_source,
            routing_status=routing_status,
            ignored_reason=ignored_reason,
            run_id=run_id,
        )

        webhook_automation_receipt_response.additional_properties = d
        return webhook_automation_receipt_response

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
