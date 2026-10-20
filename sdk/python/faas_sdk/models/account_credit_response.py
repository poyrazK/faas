from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountCreditResponse")


@_attrs_define
class AccountCreditResponse:
    """One row in the account_credits table (issue #279). cents_remaining
    is the integer-cents balance still available; consumption
    decrements it (the consumption reducer lands in a follow-up
    PR). expires_at is RFC 3339 when set; absent (or null) means
    the credit is valid until fully consumed. reason is the
    operator-supplied audit text (3..500 chars).

    """

    id: UUID
    account_id: UUID
    cents_remaining: int
    reason: str
    created_at: datetime.datetime
    expires_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = str(self.account_id)

        cents_remaining = self.cents_remaining

        reason = self.reason

        created_at = self.created_at.isoformat()

        expires_at: None | str | Unset
        if isinstance(self.expires_at, Unset):
            expires_at = UNSET
        elif isinstance(self.expires_at, datetime.datetime):
            expires_at = self.expires_at.isoformat()
        else:
            expires_at = self.expires_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "cents_remaining": cents_remaining,
                "reason": reason,
                "created_at": created_at,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        account_id = UUID(d.pop("account_id"))

        cents_remaining = d.pop("cents_remaining")

        reason = d.pop("reason")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_expires_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                expires_at_type_0 = datetime.datetime.fromisoformat(data)

                return expires_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        expires_at = _parse_expires_at(d.pop("expires_at", UNSET))

        account_credit_response = cls(
            id=id,
            account_id=account_id,
            cents_remaining=cents_remaining,
            reason=reason,
            created_at=created_at,
            expires_at=expires_at,
        )

        account_credit_response.additional_properties = d
        return account_credit_response

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
