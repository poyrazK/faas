from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="BillingRunUsageHoldHour")


@_attrs_define
class BillingRunUsageHoldHour:
    """Successful request counts for one UTC hour with evidence of a gap."""

    hour: datetime.datetime
    ledger_requests_at_hold: int
    telemetry_requests_at_hold: int
    required_requests: int
    """Highest successful request count captured from telemetry for this hour."""
    ledger_requests: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        hour = self.hour.isoformat()

        ledger_requests_at_hold = self.ledger_requests_at_hold

        telemetry_requests_at_hold = self.telemetry_requests_at_hold

        required_requests = self.required_requests

        ledger_requests = self.ledger_requests

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "hour": hour,
                "ledger_requests_at_hold": ledger_requests_at_hold,
                "telemetry_requests_at_hold": telemetry_requests_at_hold,
                "required_requests": required_requests,
                "ledger_requests": ledger_requests,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        hour = datetime.datetime.fromisoformat(d.pop("hour"))

        ledger_requests_at_hold = d.pop("ledger_requests_at_hold")

        telemetry_requests_at_hold = d.pop("telemetry_requests_at_hold")

        required_requests = d.pop("required_requests")

        ledger_requests = d.pop("ledger_requests")

        billing_run_usage_hold_hour = cls(
            hour=hour,
            ledger_requests_at_hold=ledger_requests_at_hold,
            telemetry_requests_at_hold=telemetry_requests_at_hold,
            required_requests=required_requests,
            ledger_requests=ledger_requests,
        )

        billing_run_usage_hold_hour.additional_properties = d
        return billing_run_usage_hold_hour

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
