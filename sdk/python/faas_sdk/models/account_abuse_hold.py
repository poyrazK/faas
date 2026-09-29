from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_abuse_hold_reason import AccountAbuseHoldReason, check_account_abuse_hold_reason

T = TypeVar("T", bound="AccountAbuseHold")


@_attrs_define
class AccountAbuseHold:
    """Present while the account is on an abuse hold (ADR-361): outbound traffic from its workloads matched a scanning or
    abuse pattern, so nothing runs or deploys until an operator releases it.

    """

    reason: AccountAbuseHoldReason
    held_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason: str = self.reason

        held_at = self.held_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reason": reason,
                "held_at": held_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = check_account_abuse_hold_reason(d.pop("reason"))

        held_at = datetime.datetime.fromisoformat(d.pop("held_at"))

        account_abuse_hold = cls(
            reason=reason,
            held_at=held_at,
        )

        account_abuse_hold.additional_properties = d
        return account_abuse_hold

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
