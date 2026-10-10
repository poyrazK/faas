from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_consumer_usage_completeness_response_status import (
    APIConsumerUsageCompletenessResponseStatus,
    check_api_consumer_usage_completeness_response_status,
)

T = TypeVar("T", bound="APIConsumerUsageCompletenessResponse")


@_attrs_define
class APIConsumerUsageCompletenessResponse:
    """Comparison of an API consumer's billing ledger with request telemetry over successful requests (ADR-941)."""

    consumer_id: UUID
    status: APIConsumerUsageCompletenessResponseStatus
    """gaps_detected means telemetry saw successful requests the ledger never billed; partial means telemetry
    confirms only part of the billed requests; unverifiable means telemetry holds no evidence for them."""
    checked_from: datetime.datetime
    """First whole UTC hour checked, clamped to request telemetry retention."""
    checked_until: datetime.datetime
    """Exclusive end of the checked hours, clamped to hours that have settled."""
    ledger_requests: int
    """Successful requests in the billing ledger."""
    telemetry_requests: int
    """Successful requests in request telemetry."""
    confirmed_requests: int
    """Billed successful requests telemetry corroborates."""
    missing_requests: int
    """Lower bound of successful requests telemetry saw that the ledger lacks."""
    hours_checked: int
    hours_without_telemetry: int
    """Hours with billed requests but no telemetry."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_id = str(self.consumer_id)

        status: str = self.status

        checked_from = self.checked_from.isoformat()

        checked_until = self.checked_until.isoformat()

        ledger_requests = self.ledger_requests

        telemetry_requests = self.telemetry_requests

        confirmed_requests = self.confirmed_requests

        missing_requests = self.missing_requests

        hours_checked = self.hours_checked

        hours_without_telemetry = self.hours_without_telemetry

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumer_id": consumer_id,
                "status": status,
                "checked_from": checked_from,
                "checked_until": checked_until,
                "ledger_requests": ledger_requests,
                "telemetry_requests": telemetry_requests,
                "confirmed_requests": confirmed_requests,
                "missing_requests": missing_requests,
                "hours_checked": hours_checked,
                "hours_without_telemetry": hours_without_telemetry,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        consumer_id = UUID(d.pop("consumer_id"))

        status = check_api_consumer_usage_completeness_response_status(d.pop("status"))

        checked_from = datetime.datetime.fromisoformat(d.pop("checked_from"))

        checked_until = datetime.datetime.fromisoformat(d.pop("checked_until"))

        ledger_requests = d.pop("ledger_requests")

        telemetry_requests = d.pop("telemetry_requests")

        confirmed_requests = d.pop("confirmed_requests")

        missing_requests = d.pop("missing_requests")

        hours_checked = d.pop("hours_checked")

        hours_without_telemetry = d.pop("hours_without_telemetry")

        api_consumer_usage_completeness_response = cls(
            consumer_id=consumer_id,
            status=status,
            checked_from=checked_from,
            checked_until=checked_until,
            ledger_requests=ledger_requests,
            telemetry_requests=telemetry_requests,
            confirmed_requests=confirmed_requests,
            missing_requests=missing_requests,
            hours_checked=hours_checked,
            hours_without_telemetry=hours_without_telemetry,
        )

        api_consumer_usage_completeness_response.additional_properties = d
        return api_consumer_usage_completeness_response

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
