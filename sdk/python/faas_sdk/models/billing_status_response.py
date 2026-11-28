from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_status_response_account_status import (
    BillingStatusResponseAccountStatus,
    check_billing_status_response_account_status,
)
from ..models.billing_status_response_mode import BillingStatusResponseMode, check_billing_status_response_mode
from ..models.billing_status_response_plan import BillingStatusResponsePlan, check_billing_status_response_plan
from ..models.billing_status_response_scheduled_plan import (
    BillingStatusResponseScheduledPlan,
    check_billing_status_response_scheduled_plan,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="BillingStatusResponse")


@_attrs_define
class BillingStatusResponse:
    """Provider-independent customer billing status."""

    mode: BillingStatusResponseMode
    enabled: bool
    provider: str
    """Configured provider name; no provider-specific identifiers are exposed."""
    plan: BillingStatusResponsePlan
    account_status: BillingStatusResponseAccountStatus
    customer_configured: bool
    subscription_configured: bool
    usage_reconciliation_enabled: bool
    subscription_ends_at: datetime.datetime | Unset = UNSET
    """When a scheduled cancellation ends the subscription instead of renewing it; the plan then returns to Free.
    Absent when none is scheduled."""
    scheduled_plan: BillingStatusResponseScheduledPlan | Unset = UNSET
    """The plan a scheduled downgrade moves to at scheduled_plan_at. Absent when none is scheduled."""
    scheduled_plan_at: datetime.datetime | Unset = UNSET
    """When the scheduled downgrade applies."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        enabled = self.enabled

        provider = self.provider

        plan: str = self.plan

        account_status: str = self.account_status

        customer_configured = self.customer_configured

        subscription_configured = self.subscription_configured

        usage_reconciliation_enabled = self.usage_reconciliation_enabled

        subscription_ends_at: str | Unset = UNSET
        if not isinstance(self.subscription_ends_at, Unset):
            subscription_ends_at = self.subscription_ends_at.isoformat()

        scheduled_plan: str | Unset = UNSET
        if not isinstance(self.scheduled_plan, Unset):
            scheduled_plan = self.scheduled_plan

        scheduled_plan_at: str | Unset = UNSET
        if not isinstance(self.scheduled_plan_at, Unset):
            scheduled_plan_at = self.scheduled_plan_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "enabled": enabled,
                "provider": provider,
                "plan": plan,
                "account_status": account_status,
                "customer_configured": customer_configured,
                "subscription_configured": subscription_configured,
                "usage_reconciliation_enabled": usage_reconciliation_enabled,
            }
        )
        if subscription_ends_at is not UNSET:
            field_dict["subscription_ends_at"] = subscription_ends_at
        if scheduled_plan is not UNSET:
            field_dict["scheduled_plan"] = scheduled_plan
        if scheduled_plan_at is not UNSET:
            field_dict["scheduled_plan_at"] = scheduled_plan_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_billing_status_response_mode(d.pop("mode"))

        enabled = d.pop("enabled")

        provider = d.pop("provider")

        plan = check_billing_status_response_plan(d.pop("plan"))

        account_status = check_billing_status_response_account_status(d.pop("account_status"))

        customer_configured = d.pop("customer_configured")

        subscription_configured = d.pop("subscription_configured")

        usage_reconciliation_enabled = d.pop("usage_reconciliation_enabled")

        _subscription_ends_at = d.pop("subscription_ends_at", UNSET)
        subscription_ends_at: datetime.datetime | Unset
        if isinstance(_subscription_ends_at, Unset):
            subscription_ends_at = UNSET
        else:
            subscription_ends_at = datetime.datetime.fromisoformat(_subscription_ends_at)

        _scheduled_plan = d.pop("scheduled_plan", UNSET)
        scheduled_plan: BillingStatusResponseScheduledPlan | Unset
        if isinstance(_scheduled_plan, Unset):
            scheduled_plan = UNSET
        else:
            scheduled_plan = check_billing_status_response_scheduled_plan(_scheduled_plan)

        _scheduled_plan_at = d.pop("scheduled_plan_at", UNSET)
        scheduled_plan_at: datetime.datetime | Unset
        if isinstance(_scheduled_plan_at, Unset):
            scheduled_plan_at = UNSET
        else:
            scheduled_plan_at = datetime.datetime.fromisoformat(_scheduled_plan_at)

        billing_status_response = cls(
            mode=mode,
            enabled=enabled,
            provider=provider,
            plan=plan,
            account_status=account_status,
            customer_configured=customer_configured,
            subscription_configured=subscription_configured,
            usage_reconciliation_enabled=usage_reconciliation_enabled,
            subscription_ends_at=subscription_ends_at,
            scheduled_plan=scheduled_plan,
            scheduled_plan_at=scheduled_plan_at,
        )

        billing_status_response.additional_properties = d
        return billing_status_response

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
