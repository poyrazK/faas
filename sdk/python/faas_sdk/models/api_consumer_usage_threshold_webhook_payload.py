from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APIConsumerUsageThresholdWebhookPayload")


@_attrs_define
class APIConsumerUsageThresholdWebhookPayload:
    """Data delivered with consumer.usage_threshold when a consumer crosses a plan alert threshold. alert_id is stable across
    retries.
    """

    alert_id: UUID
    app_id: UUID
    consumer_id: UUID
    external_ref: str
    plan_id: UUID
    threshold_percent: int
    limit_units: int
    used_units: int
    month_start: datetime.datetime
    crossed_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        alert_id = str(self.alert_id)

        app_id = str(self.app_id)

        consumer_id = str(self.consumer_id)

        external_ref = self.external_ref

        plan_id = str(self.plan_id)

        threshold_percent = self.threshold_percent

        limit_units = self.limit_units

        used_units = self.used_units

        month_start = self.month_start.isoformat()

        crossed_at = self.crossed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "alert_id": alert_id,
                "app_id": app_id,
                "consumer_id": consumer_id,
                "external_ref": external_ref,
                "plan_id": plan_id,
                "threshold_percent": threshold_percent,
                "limit_units": limit_units,
                "used_units": used_units,
                "month_start": month_start,
                "crossed_at": crossed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        alert_id = UUID(d.pop("alert_id"))

        app_id = UUID(d.pop("app_id"))

        consumer_id = UUID(d.pop("consumer_id"))

        external_ref = d.pop("external_ref")

        plan_id = UUID(d.pop("plan_id"))

        threshold_percent = d.pop("threshold_percent")

        limit_units = d.pop("limit_units")

        used_units = d.pop("used_units")

        month_start = datetime.datetime.fromisoformat(d.pop("month_start"))

        crossed_at = datetime.datetime.fromisoformat(d.pop("crossed_at"))

        api_consumer_usage_threshold_webhook_payload = cls(
            alert_id=alert_id,
            app_id=app_id,
            consumer_id=consumer_id,
            external_ref=external_ref,
            plan_id=plan_id,
            threshold_percent=threshold_percent,
            limit_units=limit_units,
            used_units=used_units,
            month_start=month_start,
            crossed_at=crossed_at,
        )

        api_consumer_usage_threshold_webhook_payload.additional_properties = d
        return api_consumer_usage_threshold_webhook_payload

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
