from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_delivery_response_readiness_destination import (
    AlertDeliveryResponseReadinessDestination,
    check_alert_delivery_response_readiness_destination,
)
from ..models.alert_delivery_response_readiness_failover_reason import (
    AlertDeliveryResponseReadinessFailoverReason,
    check_alert_delivery_response_readiness_failover_reason,
)
from ..models.alert_delivery_response_status import AlertDeliveryResponseStatus, check_alert_delivery_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="AlertDeliveryResponse")


@_attrs_define
class AlertDeliveryResponse:
    """One alert_deliveries row as surfaced by
    GET /v1/apps/{slug}/alerts/{id}/deliveries.
    Test rows (IsTest=true) are written by Dispatcher.DispatchTest
    (the customer-facing "send test alert" path); the production
    read (include_test=false) hides them, the operator read
    (?include_test=true) surfaces them.

    """

    id: UUID
    rule_id: UUID
    account_id: UUID
    idempotency_key: str
    """rule_id + ':' + cooldown bucket (production) or delivery_id + ':test' (test path)"""
    status: AlertDeliveryResponseStatus
    attempt_count: int
    observed_value: float
    fired_at: datetime.datetime
    is_test: bool
    """true iff the row was written by Dispatcher.DispatchTest"""
    readiness_event_id: UUID | Unset = UNSET
    """Stable readiness logical event associated with this physical delivery."""
    readiness_destination: AlertDeliveryResponseReadinessDestination | Unset = UNSET
    """Selected receiver role for this physical readiness dispatch."""
    readiness_failover_reason: AlertDeliveryResponseReadinessFailoverReason | Unset = UNSET
    """Destination selection reason recorded on this physical readiness delivery."""
    app_id: UUID | Unset = UNSET
    """omitted on account-wide rules"""
    last_status_code: int | Unset = UNSET
    """0 when the attempt never reached the wire"""
    last_error: str | Unset = UNSET
    """truncated server-side via dashboard.FormatAlertError (log-injection-safe)"""
    delivered_at: datetime.datetime | Unset = UNSET
    """omitted until status=delivered"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        rule_id = str(self.rule_id)

        account_id = str(self.account_id)

        idempotency_key = self.idempotency_key

        status: str = self.status

        attempt_count = self.attempt_count

        observed_value = self.observed_value

        fired_at = self.fired_at.isoformat()

        is_test = self.is_test

        readiness_event_id: str | Unset = UNSET
        if not isinstance(self.readiness_event_id, Unset):
            readiness_event_id = str(self.readiness_event_id)

        readiness_destination: str | Unset = UNSET
        if not isinstance(self.readiness_destination, Unset):
            readiness_destination = self.readiness_destination

        readiness_failover_reason: str | Unset = UNSET
        if not isinstance(self.readiness_failover_reason, Unset):
            readiness_failover_reason = self.readiness_failover_reason

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        last_status_code = self.last_status_code

        last_error = self.last_error

        delivered_at: str | Unset = UNSET
        if not isinstance(self.delivered_at, Unset):
            delivered_at = self.delivered_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "rule_id": rule_id,
                "account_id": account_id,
                "idempotency_key": idempotency_key,
                "status": status,
                "attempt_count": attempt_count,
                "observed_value": observed_value,
                "fired_at": fired_at,
                "is_test": is_test,
            }
        )
        if readiness_event_id is not UNSET:
            field_dict["readiness_event_id"] = readiness_event_id
        if readiness_destination is not UNSET:
            field_dict["readiness_destination"] = readiness_destination
        if readiness_failover_reason is not UNSET:
            field_dict["readiness_failover_reason"] = readiness_failover_reason
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if last_status_code is not UNSET:
            field_dict["last_status_code"] = last_status_code
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if delivered_at is not UNSET:
            field_dict["delivered_at"] = delivered_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        rule_id = UUID(d.pop("rule_id"))

        account_id = UUID(d.pop("account_id"))

        idempotency_key = d.pop("idempotency_key")

        status = check_alert_delivery_response_status(d.pop("status"))

        attempt_count = d.pop("attempt_count")

        observed_value = d.pop("observed_value")

        fired_at = datetime.datetime.fromisoformat(d.pop("fired_at"))

        is_test = d.pop("is_test")

        _readiness_event_id = d.pop("readiness_event_id", UNSET)
        readiness_event_id: UUID | Unset
        if isinstance(_readiness_event_id, Unset):
            readiness_event_id = UNSET
        else:
            readiness_event_id = UUID(_readiness_event_id)

        _readiness_destination = d.pop("readiness_destination", UNSET)
        readiness_destination: AlertDeliveryResponseReadinessDestination | Unset
        if isinstance(_readiness_destination, Unset):
            readiness_destination = UNSET
        else:
            readiness_destination = check_alert_delivery_response_readiness_destination(_readiness_destination)

        _readiness_failover_reason = d.pop("readiness_failover_reason", UNSET)
        readiness_failover_reason: AlertDeliveryResponseReadinessFailoverReason | Unset
        if isinstance(_readiness_failover_reason, Unset):
            readiness_failover_reason = UNSET
        else:
            readiness_failover_reason = check_alert_delivery_response_readiness_failover_reason(
                _readiness_failover_reason
            )

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        last_status_code = d.pop("last_status_code", UNSET)

        last_error = d.pop("last_error", UNSET)

        _delivered_at = d.pop("delivered_at", UNSET)
        delivered_at: datetime.datetime | Unset
        if isinstance(_delivered_at, Unset):
            delivered_at = UNSET
        else:
            delivered_at = datetime.datetime.fromisoformat(_delivered_at)

        alert_delivery_response = cls(
            id=id,
            rule_id=rule_id,
            account_id=account_id,
            idempotency_key=idempotency_key,
            status=status,
            attempt_count=attempt_count,
            observed_value=observed_value,
            fired_at=fired_at,
            is_test=is_test,
            readiness_event_id=readiness_event_id,
            readiness_destination=readiness_destination,
            readiness_failover_reason=readiness_failover_reason,
            app_id=app_id,
            last_status_code=last_status_code,
            last_error=last_error,
            delivered_at=delivered_at,
        )

        alert_delivery_response.additional_properties = d
        return alert_delivery_response

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
