from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_budget_threshold_webhook_payload_budget import (
    OutboundAIBudgetThresholdWebhookPayloadBudget,
    check_outbound_ai_budget_threshold_webhook_payload_budget,
)
from ..models.outbound_ai_budget_threshold_webhook_payload_threshold_percent import (
    OutboundAIBudgetThresholdWebhookPayloadThresholdPercent,
    check_outbound_ai_budget_threshold_webhook_payload_threshold_percent,
)

T = TypeVar("T", bound="OutboundAIBudgetThresholdWebhookPayload")


@_attrs_define
class OutboundAIBudgetThresholdWebhookPayload:
    """Data delivered with outbound.ai_budget.threshold when an AI budget reaches 80% or 100% in its period. alert_id is
    stable across retries; integration budgets notify every bound app, app budgets only that app, and an end user's or a
    tag's budget only the app whose call crossed it.

    """

    alert_id: UUID
    integration_id: UUID
    integration_name: str
    budget: OutboundAIBudgetThresholdWebhookPayloadBudget
    app_id: None | UUID
    end_user: None | str
    """The X-Gregale-AI-User value whose daily budget crossed the threshold."""
    period_start: datetime.date
    threshold_percent: OutboundAIBudgetThresholdWebhookPayloadThresholdPercent
    limit: int
    used: int
    crossed_at: datetime.datetime
    tag: None | str
    """The tag whose daily cost limit crossed the threshold."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        alert_id = str(self.alert_id)

        integration_id = str(self.integration_id)

        integration_name = self.integration_name

        budget: str = self.budget

        app_id: None | str
        if isinstance(self.app_id, UUID):
            app_id = str(self.app_id)
        else:
            app_id = self.app_id

        end_user: None | str
        end_user = self.end_user

        period_start = self.period_start.isoformat()

        threshold_percent: int = self.threshold_percent

        limit = self.limit

        used = self.used

        crossed_at = self.crossed_at.isoformat()

        tag: None | str
        tag = self.tag

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "alert_id": alert_id,
                "integration_id": integration_id,
                "integration_name": integration_name,
                "budget": budget,
                "app_id": app_id,
                "end_user": end_user,
                "period_start": period_start,
                "threshold_percent": threshold_percent,
                "limit": limit,
                "used": used,
                "crossed_at": crossed_at,
                "tag": tag,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        alert_id = UUID(d.pop("alert_id"))

        integration_id = UUID(d.pop("integration_id"))

        integration_name = d.pop("integration_name")

        budget = check_outbound_ai_budget_threshold_webhook_payload_budget(d.pop("budget"))

        def _parse_app_id(data: object) -> None | UUID:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                app_id_type_0 = UUID(data)

                return app_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | UUID, data)

        app_id = _parse_app_id(d.pop("app_id"))

        def _parse_end_user(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        end_user = _parse_end_user(d.pop("end_user"))

        period_start = datetime.date.fromisoformat(d.pop("period_start"))

        threshold_percent = check_outbound_ai_budget_threshold_webhook_payload_threshold_percent(
            d.pop("threshold_percent")
        )

        limit = d.pop("limit")

        used = d.pop("used")

        crossed_at = datetime.datetime.fromisoformat(d.pop("crossed_at"))

        def _parse_tag(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        tag = _parse_tag(d.pop("tag"))

        outbound_ai_budget_threshold_webhook_payload = cls(
            alert_id=alert_id,
            integration_id=integration_id,
            integration_name=integration_name,
            budget=budget,
            app_id=app_id,
            end_user=end_user,
            period_start=period_start,
            threshold_percent=threshold_percent,
            limit=limit,
            used=used,
            crossed_at=crossed_at,
            tag=tag,
        )

        outbound_ai_budget_threshold_webhook_payload.additional_properties = d
        return outbound_ai_budget_threshold_webhook_payload

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
