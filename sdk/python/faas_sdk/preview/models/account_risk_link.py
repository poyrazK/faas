from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_risk_link_signal import AccountRiskLinkSignal, check_account_risk_link_signal
from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountRiskLink")


@_attrs_define
class AccountRiskLink:
    """A new account linked to an abuse-held account."""

    account_id: str
    held_account_id: str
    held_reason: str
    signal: AccountRiskLinkSignal
    detected_at: datetime.datetime
    held_org_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = self.account_id

        held_account_id = self.held_account_id

        held_reason = self.held_reason

        signal: str = self.signal

        detected_at = self.detected_at.isoformat()

        held_org_id = self.held_org_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "held_account_id": held_account_id,
                "held_reason": held_reason,
                "signal": signal,
                "detected_at": detected_at,
            }
        )
        if held_org_id is not UNSET:
            field_dict["held_org_id"] = held_org_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = d.pop("account_id")

        held_account_id = d.pop("held_account_id")

        held_reason = d.pop("held_reason")

        signal = check_account_risk_link_signal(d.pop("signal"))

        detected_at = datetime.datetime.fromisoformat(d.pop("detected_at"))

        held_org_id = d.pop("held_org_id", UNSET)

        account_risk_link = cls(
            account_id=account_id,
            held_account_id=held_account_id,
            held_reason=held_reason,
            signal=signal,
            detected_at=detected_at,
            held_org_id=held_org_id,
        )

        account_risk_link.additional_properties = d
        return account_risk_link

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
