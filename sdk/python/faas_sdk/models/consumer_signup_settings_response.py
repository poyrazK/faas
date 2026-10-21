from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.consumer_signup_settings_response_provider import (
    ConsumerSignupSettingsResponseProvider,
    check_consumer_signup_settings_response_provider,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ConsumerSignupSettingsResponse")


@_attrs_define
class ConsumerSignupSettingsResponse:
    """An app's sign-up settings."""

    app: str
    enabled: bool
    provider: ConsumerSignupSettingsResponseProvider
    plan_ids: list[UUID]
    key_scopes: list[str]
    display_name: str
    daily_limit: int
    page_url: str
    """The hosted sign-up page to link customers to."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    terms_url: str | Unset = UNSET
    return_url: str | Unset = UNSET
    trial_days: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        enabled = self.enabled

        provider: str = self.provider

        plan_ids = []
        for plan_ids_item_data in self.plan_ids:
            plan_ids_item = str(plan_ids_item_data)
            plan_ids.append(plan_ids_item)

        key_scopes = self.key_scopes

        display_name = self.display_name

        daily_limit = self.daily_limit

        page_url = self.page_url

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        terms_url = self.terms_url

        return_url = self.return_url

        trial_days = self.trial_days

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "enabled": enabled,
                "provider": provider,
                "plan_ids": plan_ids,
                "key_scopes": key_scopes,
                "display_name": display_name,
                "daily_limit": daily_limit,
                "page_url": page_url,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if terms_url is not UNSET:
            field_dict["terms_url"] = terms_url
        if return_url is not UNSET:
            field_dict["return_url"] = return_url
        if trial_days is not UNSET:
            field_dict["trial_days"] = trial_days

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app = d.pop("app")

        enabled = d.pop("enabled")

        provider = check_consumer_signup_settings_response_provider(d.pop("provider"))

        plan_ids = []
        _plan_ids = d.pop("plan_ids")
        for plan_ids_item_data in _plan_ids:
            plan_ids_item = UUID(plan_ids_item_data)

            plan_ids.append(plan_ids_item)

        key_scopes = cast(list[str], d.pop("key_scopes"))

        display_name = d.pop("display_name")

        daily_limit = d.pop("daily_limit")

        page_url = d.pop("page_url")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        terms_url = d.pop("terms_url", UNSET)

        return_url = d.pop("return_url", UNSET)

        trial_days = d.pop("trial_days", UNSET)

        consumer_signup_settings_response = cls(
            app=app,
            enabled=enabled,
            provider=provider,
            plan_ids=plan_ids,
            key_scopes=key_scopes,
            display_name=display_name,
            daily_limit=daily_limit,
            page_url=page_url,
            created_at=created_at,
            updated_at=updated_at,
            terms_url=terms_url,
            return_url=return_url,
            trial_days=trial_days,
        )

        consumer_signup_settings_response.additional_properties = d
        return consumer_signup_settings_response

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
