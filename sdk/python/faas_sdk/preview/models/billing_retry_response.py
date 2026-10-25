from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="BillingRetryResponse")


@_attrs_define
class BillingRetryResponse:
    """Returned by POST /v1/billing/retry (issue #242). The CLI
    prints attempt_id and provider_ref_id so the customer can
    quote them to support if the charge still fails. status is
    "pending_provider_confirmation" — the CLI does not poll for
    a settlement flip; that flip arrives via the provider's
    webhook (EventPaymentSucceeded) and is rendered by the
    dashboard / dunning email pipeline.

    """

    attempt_id: str
    """apId-side attempt handle (Stripe: in_…, Paddle: txn_…-related)."""
    provider_ref_id: str
    """Provider-side handle for the new attempt (Stripe: pi_…, Paddle: tx_…)."""
    status: str
    """Always "pending_provider_confirmation" today; reserved for future settlement states."""
    next_billing_at: datetime.datetime | None | Unset = UNSET
    """Next scheduled billing instant after the retry settles; null when not known yet."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempt_id = self.attempt_id

        provider_ref_id = self.provider_ref_id

        status = self.status

        next_billing_at: None | str | Unset
        if isinstance(self.next_billing_at, Unset):
            next_billing_at = UNSET
        elif isinstance(self.next_billing_at, datetime.datetime):
            next_billing_at = self.next_billing_at.isoformat()
        else:
            next_billing_at = self.next_billing_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempt_id": attempt_id,
                "provider_ref_id": provider_ref_id,
                "status": status,
            }
        )
        if next_billing_at is not UNSET:
            field_dict["next_billing_at"] = next_billing_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempt_id = d.pop("attempt_id")

        provider_ref_id = d.pop("provider_ref_id")

        status = d.pop("status")

        def _parse_next_billing_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                next_billing_at_type_0 = datetime.datetime.fromisoformat(data)

                return next_billing_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        next_billing_at = _parse_next_billing_at(d.pop("next_billing_at", UNSET))

        billing_retry_response = cls(
            attempt_id=attempt_id,
            provider_ref_id=provider_ref_id,
            status=status,
            next_billing_at=next_billing_at,
        )

        billing_retry_response.additional_properties = d
        return billing_retry_response

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
