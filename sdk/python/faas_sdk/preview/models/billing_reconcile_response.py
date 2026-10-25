from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="BillingReconcileResponse")


@_attrs_define
class BillingReconcileResponse:
    """Returned by POST /v1/admin/billing-reconcile/{id} (PR-P3).
    mb_seconds is the integer total the provider SDK returned
    for the rolling 30-day window [start, end). Operators can
    diff the SDK-side number against the local usage_minutes
    sum to spot drift between the platform's billing source
    of truth and the provider's ledger.

    """

    account_id: UUID
    start: datetime.datetime
    """Window start (RFC 3339, UTC)."""
    end: datetime.datetime
    """Window end (RFC 3339, UTC)."""
    mb_seconds: int
    """Integer mb_seconds total for the window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        start = self.start.isoformat()

        end = self.end.isoformat()

        mb_seconds = self.mb_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "start": start,
                "end": end,
                "mb_seconds": mb_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        start = datetime.datetime.fromisoformat(d.pop("start"))

        end = datetime.datetime.fromisoformat(d.pop("end"))

        mb_seconds = d.pop("mb_seconds")

        billing_reconcile_response = cls(
            account_id=account_id,
            start=start,
            end=end,
            mb_seconds=mb_seconds,
        )

        billing_reconcile_response.additional_properties = d
        return billing_reconcile_response

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
