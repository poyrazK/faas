from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.consumer_signup_response_status import ConsumerSignupResponseStatus, check_consumer_signup_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="ConsumerSignupResponse")


@_attrs_define
class ConsumerSignupResponse:
    """One sign-up as the app owner sees it."""

    id: UUID
    email: str
    name: str
    status: ConsumerSignupResponseStatus
    livemode: bool
    created_at: datetime.datetime
    expires_at: datetime.datetime
    plan_id: UUID | Unset = UNSET
    provider_customer_id: str | Unset = UNSET
    """The Stripe customer, also the consumer's external_ref."""
    consumer_id: UUID | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        email = self.email

        name = self.name

        status: str = self.status

        livemode = self.livemode

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        plan_id: str | Unset = UNSET
        if not isinstance(self.plan_id, Unset):
            plan_id = str(self.plan_id)

        provider_customer_id = self.provider_customer_id

        consumer_id: str | Unset = UNSET
        if not isinstance(self.consumer_id, Unset):
            consumer_id = str(self.consumer_id)

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "email": email,
                "name": name,
                "status": status,
                "livemode": livemode,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if plan_id is not UNSET:
            field_dict["plan_id"] = plan_id
        if provider_customer_id is not UNSET:
            field_dict["provider_customer_id"] = provider_customer_id
        if consumer_id is not UNSET:
            field_dict["consumer_id"] = consumer_id
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        email = d.pop("email")

        name = d.pop("name")

        status = check_consumer_signup_response_status(d.pop("status"))

        livemode = d.pop("livemode")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _plan_id = d.pop("plan_id", UNSET)
        plan_id: UUID | Unset
        if isinstance(_plan_id, Unset):
            plan_id = UNSET
        else:
            plan_id = UUID(_plan_id)

        provider_customer_id = d.pop("provider_customer_id", UNSET)

        _consumer_id = d.pop("consumer_id", UNSET)
        consumer_id: UUID | Unset
        if isinstance(_consumer_id, Unset):
            consumer_id = UNSET
        else:
            consumer_id = UUID(_consumer_id)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        consumer_signup_response = cls(
            id=id,
            email=email,
            name=name,
            status=status,
            livemode=livemode,
            created_at=created_at,
            expires_at=expires_at,
            plan_id=plan_id,
            provider_customer_id=provider_customer_id,
            consumer_id=consumer_id,
            completed_at=completed_at,
        )

        consumer_signup_response.additional_properties = d
        return consumer_signup_response

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
