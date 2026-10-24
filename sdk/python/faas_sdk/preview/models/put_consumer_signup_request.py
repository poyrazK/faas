from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.put_consumer_signup_request_key_scopes_item import (
    PutConsumerSignupRequestKeyScopesItem,
    check_put_consumer_signup_request_key_scopes_item,
)
from ..models.put_consumer_signup_request_provider import (
    PutConsumerSignupRequestProvider,
    check_put_consumer_signup_request_provider,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PutConsumerSignupRequest")


@_attrs_define
class PutConsumerSignupRequest:
    """Self-service sign-up settings (ADR-20261021143000). Omitted fields take their defaults."""

    display_name: str
    enabled: bool | Unset = UNSET
    """Whether customers can start sign-ups (default true)."""
    provider: PutConsumerSignupRequestProvider | Unset = UNSET
    """Where cards are saved (default stripe)."""
    plan_ids: list[UUID] | Unset = UNSET
    """Consumer plans customers choose from; none offers the app's default plan."""
    key_scopes: list[PutConsumerSignupRequestKeyScopesItem] | Unset = UNSET
    """Scopes of the key a customer gets (default write and billing)."""
    terms_url: str | Unset = UNSET
    """https URL customers accept before signing up."""
    return_url: str | Unset = UNSET
    """https URL shown after the key; it never carries the key."""
    trial_days: int | Unset = UNSET
    daily_limit: int | Unset = UNSET
    """Sign-ups per UTC day (default 100)."""
    customer_plan_changes: bool | Unset = UNSET
    """Let customers switch between plan_ids with their own key (ADR-20261021183000)."""
    customer_closing: bool | Unset = UNSET
    """Let customers close their account with their own key (ADR-20261021183000)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        display_name = self.display_name

        enabled = self.enabled

        provider: str | Unset = UNSET
        if not isinstance(self.provider, Unset):
            provider = self.provider

        plan_ids: list[str] | Unset = UNSET
        if not isinstance(self.plan_ids, Unset):
            plan_ids = []
            for plan_ids_item_data in self.plan_ids:
                plan_ids_item = str(plan_ids_item_data)
                plan_ids.append(plan_ids_item)

        key_scopes: list[str] | Unset = UNSET
        if not isinstance(self.key_scopes, Unset):
            key_scopes = []
            for key_scopes_item_data in self.key_scopes:
                key_scopes_item: str = key_scopes_item_data
                key_scopes.append(key_scopes_item)

        terms_url = self.terms_url

        return_url = self.return_url

        trial_days = self.trial_days

        daily_limit = self.daily_limit

        customer_plan_changes = self.customer_plan_changes

        customer_closing = self.customer_closing

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "display_name": display_name,
            }
        )
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if provider is not UNSET:
            field_dict["provider"] = provider
        if plan_ids is not UNSET:
            field_dict["plan_ids"] = plan_ids
        if key_scopes is not UNSET:
            field_dict["key_scopes"] = key_scopes
        if terms_url is not UNSET:
            field_dict["terms_url"] = terms_url
        if return_url is not UNSET:
            field_dict["return_url"] = return_url
        if trial_days is not UNSET:
            field_dict["trial_days"] = trial_days
        if daily_limit is not UNSET:
            field_dict["daily_limit"] = daily_limit
        if customer_plan_changes is not UNSET:
            field_dict["customer_plan_changes"] = customer_plan_changes
        if customer_closing is not UNSET:
            field_dict["customer_closing"] = customer_closing

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        display_name = d.pop("display_name")

        enabled = d.pop("enabled", UNSET)

        _provider = d.pop("provider", UNSET)
        provider: PutConsumerSignupRequestProvider | Unset
        if isinstance(_provider, Unset):
            provider = UNSET
        else:
            provider = check_put_consumer_signup_request_provider(_provider)

        _plan_ids = d.pop("plan_ids", UNSET)
        plan_ids: list[UUID] | Unset = UNSET
        if _plan_ids is not UNSET:
            plan_ids = []
            for plan_ids_item_data in _plan_ids:
                plan_ids_item = UUID(plan_ids_item_data)

                plan_ids.append(plan_ids_item)

        _key_scopes = d.pop("key_scopes", UNSET)
        key_scopes: list[PutConsumerSignupRequestKeyScopesItem] | Unset = UNSET
        if _key_scopes is not UNSET:
            key_scopes = []
            for key_scopes_item_data in _key_scopes:
                key_scopes_item = check_put_consumer_signup_request_key_scopes_item(key_scopes_item_data)

                key_scopes.append(key_scopes_item)

        terms_url = d.pop("terms_url", UNSET)

        return_url = d.pop("return_url", UNSET)

        trial_days = d.pop("trial_days", UNSET)

        daily_limit = d.pop("daily_limit", UNSET)

        customer_plan_changes = d.pop("customer_plan_changes", UNSET)

        customer_closing = d.pop("customer_closing", UNSET)

        put_consumer_signup_request = cls(
            display_name=display_name,
            enabled=enabled,
            provider=provider,
            plan_ids=plan_ids,
            key_scopes=key_scopes,
            terms_url=terms_url,
            return_url=return_url,
            trial_days=trial_days,
            daily_limit=daily_limit,
            customer_plan_changes=customer_plan_changes,
            customer_closing=customer_closing,
        )

        put_consumer_signup_request.additional_properties = d
        return put_consumer_signup_request

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
