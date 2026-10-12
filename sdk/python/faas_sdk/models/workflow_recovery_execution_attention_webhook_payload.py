from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_execution_attention import WorkflowRecoveryExecutionAttention


T = TypeVar("T", bound="WorkflowRecoveryExecutionAttentionWebhookPayload")


@_attrs_define
class WorkflowRecoveryExecutionAttentionWebhookPayload:
    """First execution attention signal captured for a native recovery job with frozen notification receivers."""

    event_id: UUID
    job_id: UUID
    app_id: UUID
    observed_at: datetime.datetime
    attention: WorkflowRecoveryExecutionAttention
    """Generation-fenced advisory execution monitoring. Capacity and scheduled wait counters describe underlying
    waits and may overlap deadline exceeded counts. Only the first attention event per job is captured. Detection
    never cancels or retries work."""
    items_url: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        job_id = str(self.job_id)

        app_id = str(self.app_id)

        observed_at = self.observed_at.isoformat()

        attention = self.attention.to_dict()

        items_url = self.items_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "job_id": job_id,
                "app_id": app_id,
                "observed_at": observed_at,
                "attention": attention,
                "items_url": items_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_execution_attention import WorkflowRecoveryExecutionAttention

        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        job_id = UUID(d.pop("job_id"))

        app_id = UUID(d.pop("app_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        attention = WorkflowRecoveryExecutionAttention.from_dict(d.pop("attention"))

        items_url = d.pop("items_url")

        workflow_recovery_execution_attention_webhook_payload = cls(
            event_id=event_id,
            job_id=job_id,
            app_id=app_id,
            observed_at=observed_at,
            attention=attention,
            items_url=items_url,
        )

        workflow_recovery_execution_attention_webhook_payload.additional_properties = d
        return workflow_recovery_execution_attention_webhook_payload

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
