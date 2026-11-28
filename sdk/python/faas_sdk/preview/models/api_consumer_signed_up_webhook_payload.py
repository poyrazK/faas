from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="APIConsumerSignedUpWebhookPayload")


@_attrs_define
class APIConsumerSignedUpWebhookPayload:
    """Data delivered with consumer.signed_up once a customer's sign-up created their consumer. signup_id is stable across
    retries.

    """

    signup_id: UUID
    app_id: UUID
    consumer_id: UUID
    external_ref: str
    """The customer's Stripe customer."""
    name: str
    email: str
    livemode: bool
    signed_up_at: datetime.datetime
    plan_id: UUID | Unset = UNSET
    trial_ends_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        signup_id = str(self.signup_id)

        app_id = str(self.app_id)

        consumer_id = str(self.consumer_id)

        external_ref = self.external_ref

        name = self.name

        email = self.email

        livemode = self.livemode

        signed_up_at = self.signed_up_at.isoformat()

        plan_id: str | Unset = UNSET
        if not isinstance(self.plan_id, Unset):
            plan_id = str(self.plan_id)

        trial_ends_at: str | Unset = UNSET
        if not isinstance(self.trial_ends_at, Unset):
            trial_ends_at = self.trial_ends_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "signup_id": signup_id,
                "app_id": app_id,
                "consumer_id": consumer_id,
                "external_ref": external_ref,
                "name": name,
                "email": email,
                "livemode": livemode,
                "signed_up_at": signed_up_at,
            }
        )
        if plan_id is not UNSET:
            field_dict["plan_id"] = plan_id
        if trial_ends_at is not UNSET:
            field_dict["trial_ends_at"] = trial_ends_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        signup_id = UUID(d.pop("signup_id"))

        app_id = UUID(d.pop("app_id"))

        consumer_id = UUID(d.pop("consumer_id"))

        external_ref = d.pop("external_ref")

        name = d.pop("name")

        email = d.pop("email")

        livemode = d.pop("livemode")

        signed_up_at = datetime.datetime.fromisoformat(d.pop("signed_up_at"))

        _plan_id = d.pop("plan_id", UNSET)
        plan_id: UUID | Unset
        if isinstance(_plan_id, Unset):
            plan_id = UNSET
        else:
            plan_id = UUID(_plan_id)

        _trial_ends_at = d.pop("trial_ends_at", UNSET)
        trial_ends_at: datetime.datetime | Unset
        if isinstance(_trial_ends_at, Unset):
            trial_ends_at = UNSET
        else:
            trial_ends_at = datetime.datetime.fromisoformat(_trial_ends_at)

        api_consumer_signed_up_webhook_payload = cls(
            signup_id=signup_id,
            app_id=app_id,
            consumer_id=consumer_id,
            external_ref=external_ref,
            name=name,
            email=email,
            livemode=livemode,
            signed_up_at=signed_up_at,
            plan_id=plan_id,
            trial_ends_at=trial_ends_at,
        )

        api_consumer_signed_up_webhook_payload.additional_properties = d
        return api_consumer_signed_up_webhook_payload

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
