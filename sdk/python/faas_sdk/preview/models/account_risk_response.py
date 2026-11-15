from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.account_risk_event import AccountRiskEvent
    from ..models.account_risk_link import AccountRiskLink
    from ..models.account_risk_signup import AccountRiskSignup
    from ..models.account_risk_strike import AccountRiskStrike


T = TypeVar("T", bound="AccountRiskResponse")


@_attrs_define
class AccountRiskResponse:
    """An account's abuse risk. email only with include_pii."""

    account_id: str
    status: str
    plan: str
    created_at: datetime.datetime
    links_to_held: list[AccountRiskLink]
    linked_accounts: list[AccountRiskLink]
    strikes: list[AccountRiskStrike]
    abuse_events: list[AccountRiskEvent]
    email: str | Unset = UNSET
    abuse_hold_at: datetime.datetime | Unset = UNSET
    abuse_hold_reason: str | Unset = UNSET
    signup: AccountRiskSignup | Unset = UNSET
    """The account's sign-up record. signup_ip and user_agent only with include_pii, and only for 90 days."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = self.account_id

        status = self.status

        plan = self.plan

        created_at = self.created_at.isoformat()

        links_to_held = []
        for links_to_held_item_data in self.links_to_held:
            links_to_held_item = links_to_held_item_data.to_dict()
            links_to_held.append(links_to_held_item)

        linked_accounts = []
        for linked_accounts_item_data in self.linked_accounts:
            linked_accounts_item = linked_accounts_item_data.to_dict()
            linked_accounts.append(linked_accounts_item)

        strikes = []
        for strikes_item_data in self.strikes:
            strikes_item = strikes_item_data.to_dict()
            strikes.append(strikes_item)

        abuse_events = []
        for abuse_events_item_data in self.abuse_events:
            abuse_events_item = abuse_events_item_data.to_dict()
            abuse_events.append(abuse_events_item)

        email = self.email

        abuse_hold_at: str | Unset = UNSET
        if not isinstance(self.abuse_hold_at, Unset):
            abuse_hold_at = self.abuse_hold_at.isoformat()

        abuse_hold_reason = self.abuse_hold_reason

        signup: dict[str, Any] | Unset = UNSET
        if not isinstance(self.signup, Unset):
            signup = self.signup.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "status": status,
                "plan": plan,
                "created_at": created_at,
                "links_to_held": links_to_held,
                "linked_accounts": linked_accounts,
                "strikes": strikes,
                "abuse_events": abuse_events,
            }
        )
        if email is not UNSET:
            field_dict["email"] = email
        if abuse_hold_at is not UNSET:
            field_dict["abuse_hold_at"] = abuse_hold_at
        if abuse_hold_reason is not UNSET:
            field_dict["abuse_hold_reason"] = abuse_hold_reason
        if signup is not UNSET:
            field_dict["signup"] = signup

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account_risk_event import AccountRiskEvent
        from ..models.account_risk_link import AccountRiskLink
        from ..models.account_risk_signup import AccountRiskSignup
        from ..models.account_risk_strike import AccountRiskStrike

        d = dict(src_dict)
        account_id = d.pop("account_id")

        status = d.pop("status")

        plan = d.pop("plan")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        links_to_held = []
        _links_to_held = d.pop("links_to_held")
        for links_to_held_item_data in _links_to_held:
            links_to_held_item = AccountRiskLink.from_dict(links_to_held_item_data)

            links_to_held.append(links_to_held_item)

        linked_accounts = []
        _linked_accounts = d.pop("linked_accounts")
        for linked_accounts_item_data in _linked_accounts:
            linked_accounts_item = AccountRiskLink.from_dict(linked_accounts_item_data)

            linked_accounts.append(linked_accounts_item)

        strikes = []
        _strikes = d.pop("strikes")
        for strikes_item_data in _strikes:
            strikes_item = AccountRiskStrike.from_dict(strikes_item_data)

            strikes.append(strikes_item)

        abuse_events = []
        _abuse_events = d.pop("abuse_events")
        for abuse_events_item_data in _abuse_events:
            abuse_events_item = AccountRiskEvent.from_dict(abuse_events_item_data)

            abuse_events.append(abuse_events_item)

        email = d.pop("email", UNSET)

        _abuse_hold_at = d.pop("abuse_hold_at", UNSET)
        abuse_hold_at: datetime.datetime | Unset
        if isinstance(_abuse_hold_at, Unset):
            abuse_hold_at = UNSET
        else:
            abuse_hold_at = datetime.datetime.fromisoformat(_abuse_hold_at)

        abuse_hold_reason = d.pop("abuse_hold_reason", UNSET)

        _signup = d.pop("signup", UNSET)
        signup: AccountRiskSignup | Unset
        if isinstance(_signup, Unset):
            signup = UNSET
        else:
            signup = AccountRiskSignup.from_dict(_signup)

        account_risk_response = cls(
            account_id=account_id,
            status=status,
            plan=plan,
            created_at=created_at,
            links_to_held=links_to_held,
            linked_accounts=linked_accounts,
            strikes=strikes,
            abuse_events=abuse_events,
            email=email,
            abuse_hold_at=abuse_hold_at,
            abuse_hold_reason=abuse_hold_reason,
            signup=signup,
        )

        account_risk_response.additional_properties = d
        return account_risk_response

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
