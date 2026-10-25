from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="BillingCancelResponse")


@_attrs_define
class BillingCancelResponse:
    """Returned by POST /v1/billing/cancel (issue #242). The CLI
    renders effective_at as "your apps will stop on <date>".
    cancel_scheduled is always true on 200; the 409 path returns
    a Problem with a friendly "already cancelled" hint so a
    re-cancel idempotency click does not surface as a server error.

    """

    cancel_scheduled: bool
    """Always true; the absence of an active subscription returns 409 instead."""
    effective_at: datetime.datetime
    """RFC 3339 instant at which the subscription terminates (Stripe: current_period_end; Paddle: next month-
    rollover)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cancel_scheduled = self.cancel_scheduled

        effective_at = self.effective_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cancel_scheduled": cancel_scheduled,
                "effective_at": effective_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cancel_scheduled = d.pop("cancel_scheduled")

        effective_at = datetime.datetime.fromisoformat(d.pop("effective_at"))

        billing_cancel_response = cls(
            cancel_scheduled=cancel_scheduled,
            effective_at=effective_at,
        )

        billing_cancel_response.additional_properties = d
        return billing_cancel_response

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
