from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CompleteConsumerSignupResponse")


@_attrs_define
class CompleteConsumerSignupResponse:
    """The customer's consumer and new key, shown only here."""

    consumer_id: UUID
    key: str
    key_prefix: str
    scopes: list[str]
    plan_id: UUID | Unset = UNSET
    trial_ends_at: datetime.datetime | Unset = UNSET
    return_url: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_id = str(self.consumer_id)

        key = self.key

        key_prefix = self.key_prefix

        scopes = self.scopes

        plan_id: str | Unset = UNSET
        if not isinstance(self.plan_id, Unset):
            plan_id = str(self.plan_id)

        trial_ends_at: str | Unset = UNSET
        if not isinstance(self.trial_ends_at, Unset):
            trial_ends_at = self.trial_ends_at.isoformat()

        return_url = self.return_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumer_id": consumer_id,
                "key": key,
                "key_prefix": key_prefix,
                "scopes": scopes,
            }
        )
        if plan_id is not UNSET:
            field_dict["plan_id"] = plan_id
        if trial_ends_at is not UNSET:
            field_dict["trial_ends_at"] = trial_ends_at
        if return_url is not UNSET:
            field_dict["return_url"] = return_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        consumer_id = UUID(d.pop("consumer_id"))

        key = d.pop("key")

        key_prefix = d.pop("key_prefix")

        scopes = cast(list[str], d.pop("scopes"))

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

        return_url = d.pop("return_url", UNSET)

        complete_consumer_signup_response = cls(
            consumer_id=consumer_id,
            key=key,
            key_prefix=key_prefix,
            scopes=scopes,
            plan_id=plan_id,
            trial_ends_at=trial_ends_at,
            return_url=return_url,
        )

        complete_consumer_signup_response.additional_properties = d
        return complete_consumer_signup_response

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
