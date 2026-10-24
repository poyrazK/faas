from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_quality_drift_webhook_payload_metric import (
    OutboundAIQualityDriftWebhookPayloadMetric,
    check_outbound_ai_quality_drift_webhook_payload_metric,
)

T = TypeVar("T", bound="OutboundAIQualityDriftWebhookPayload")


@_attrs_define
class OutboundAIQualityDriftWebhookPayload:
    """Data delivered with outbound.ai_quality.drift to every app bound to the integration when a model's quality signal
    moves away from its previous 7 days (ADR 20261021171712). alert_id is stable across retries.

    """

    alert_id: UUID
    integration_id: UUID
    integration_name: str
    usage_date: datetime.date
    model: str
    metric: OutboundAIQualityDriftWebhookPayloadMetric
    current_value: int
    """Basis points of answered (or rated) calls; hundredths of a point for evaluation_score."""
    baseline_value: int
    current_count: int
    """Calls the current value was taken over."""
    baseline_count: int
    detected_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        alert_id = str(self.alert_id)

        integration_id = str(self.integration_id)

        integration_name = self.integration_name

        usage_date = self.usage_date.isoformat()

        model = self.model

        metric: str = self.metric

        current_value = self.current_value

        baseline_value = self.baseline_value

        current_count = self.current_count

        baseline_count = self.baseline_count

        detected_at = self.detected_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "alert_id": alert_id,
                "integration_id": integration_id,
                "integration_name": integration_name,
                "usage_date": usage_date,
                "model": model,
                "metric": metric,
                "current_value": current_value,
                "baseline_value": baseline_value,
                "current_count": current_count,
                "baseline_count": baseline_count,
                "detected_at": detected_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        alert_id = UUID(d.pop("alert_id"))

        integration_id = UUID(d.pop("integration_id"))

        integration_name = d.pop("integration_name")

        usage_date = datetime.date.fromisoformat(d.pop("usage_date"))

        model = d.pop("model")

        metric = check_outbound_ai_quality_drift_webhook_payload_metric(d.pop("metric"))

        current_value = d.pop("current_value")

        baseline_value = d.pop("baseline_value")

        current_count = d.pop("current_count")

        baseline_count = d.pop("baseline_count")

        detected_at = datetime.datetime.fromisoformat(d.pop("detected_at"))

        outbound_ai_quality_drift_webhook_payload = cls(
            alert_id=alert_id,
            integration_id=integration_id,
            integration_name=integration_name,
            usage_date=usage_date,
            model=model,
            metric=metric,
            current_value=current_value,
            baseline_value=baseline_value,
            current_count=current_count,
            baseline_count=baseline_count,
            detected_at=detected_at,
        )

        outbound_ai_quality_drift_webhook_payload.additional_properties = d
        return outbound_ai_quality_drift_webhook_payload

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
