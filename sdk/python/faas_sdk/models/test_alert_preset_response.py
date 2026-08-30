from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.test_alert_preset_response_status import (
    TestAlertPresetResponseStatus,
    check_test_alert_preset_response_status,
)

T = TypeVar("T", bound="TestAlertPresetResponse")


@_attrs_define
class TestAlertPresetResponse:
    """Response body for POST /v1/apps/{slug}/alert-presets/{name}/test
    (and the sibling dashboard form-POST handler). The handler
    dispatches a synthetic event to the customer's configured
    webhook URL with `payload.test == true`; this shape confirms
    the dispatch completed and surfaces the delivery_id the
    customer's webhook receiver should echo back.
    (issue #1233 / ADR-123 PR-C commit 2)

    """

    status: TestAlertPresetResponseStatus
    """Always "sent" on 200. A dispatch failure returns 502
    with an RFC 7807 problem document (NOT this shape).
    """
    test: bool
    """Always true on 200. Discriminator customers can key off
    in their webhook receiver to skip production alerting
    paths (e.g. PagerDuty incidents) for test dispatches.
    """
    delivery_id: str
    """32-char lowercase hex identifier for this dispatch
    (16 random bytes from crypto/rand encoded as hex). The
    audit log row (`alert_preset.test_sent`) carries the
    same delivery_id so the customer can correlate by
    timestamp + delivery_id without leaking via UUID dashes.
    """
    attempts: int
    """Number of dispatch attempts the webhookout.Dispatcher
    made before reaching the final state (1..MaxAttempts).
    Customers can use this to tune their receiver's SLA
    (e.g. a successful first attempt vs. a successful
    retry after a transient 502 from the receiver).
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        test = self.test

        delivery_id = self.delivery_id

        attempts = self.attempts

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "test": test,
                "delivery_id": delivery_id,
                "attempts": attempts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_test_alert_preset_response_status(d.pop("status"))

        test = d.pop("test")

        delivery_id = d.pop("delivery_id")

        attempts = d.pop("attempts")

        test_alert_preset_response = cls(
            status=status,
            test=test,
            delivery_id=delivery_id,
            attempts=attempts,
        )

        test_alert_preset_response.additional_properties = d
        return test_alert_preset_response

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
