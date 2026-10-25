from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="BudgetThresholdWebhookPayload")


@_attrs_define
class BudgetThresholdWebhookPayload:
    """Budget observation delivered with budget.threshold."""

    pct: float
    cap: int
    app_id: str | Unset = UNSET
    metric: str | Unset = UNSET
    observed_cents: int | Unset = UNSET
    period: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pct = self.pct

        cap = self.cap

        app_id = self.app_id

        metric = self.metric

        observed_cents = self.observed_cents

        period = self.period

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pct": pct,
                "cap": cap,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if metric is not UNSET:
            field_dict["metric"] = metric
        if observed_cents is not UNSET:
            field_dict["observed_cents"] = observed_cents
        if period is not UNSET:
            field_dict["period"] = period

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pct = d.pop("pct")

        cap = d.pop("cap")

        app_id = d.pop("app_id", UNSET)

        metric = d.pop("metric", UNSET)

        observed_cents = d.pop("observed_cents", UNSET)

        period = d.pop("period", UNSET)

        budget_threshold_webhook_payload = cls(
            pct=pct,
            cap=cap,
            app_id=app_id,
            metric=metric,
            observed_cents=observed_cents,
            period=period,
        )

        budget_threshold_webhook_payload.additional_properties = d
        return budget_threshold_webhook_payload

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
