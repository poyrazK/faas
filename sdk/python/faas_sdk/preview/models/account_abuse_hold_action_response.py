from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.account_abuse_hold import AccountAbuseHold


T = TypeVar("T", bound="AccountAbuseHoldActionResponse")


@_attrs_define
class AccountAbuseHoldActionResponse:
    """An account's abuse hold after an operator action."""

    account_id: UUID
    abuse_hold: AccountAbuseHold | None
    """The hold after the action; null when the account is not held."""
    changed: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.account_abuse_hold import AccountAbuseHold

        account_id = str(self.account_id)

        abuse_hold: dict[str, Any] | None
        if isinstance(self.abuse_hold, AccountAbuseHold):
            abuse_hold = self.abuse_hold.to_dict()
        else:
            abuse_hold = self.abuse_hold

        changed = self.changed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "abuse_hold": abuse_hold,
                "changed": changed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account_abuse_hold import AccountAbuseHold

        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        def _parse_abuse_hold(data: object) -> AccountAbuseHold | None:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                abuse_hold_type_0 = AccountAbuseHold.from_dict(data)

                return abuse_hold_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(AccountAbuseHold | None, data)

        abuse_hold = _parse_abuse_hold(d.pop("abuse_hold"))

        changed = d.pop("changed")

        account_abuse_hold_action_response = cls(
            account_id=account_id,
            abuse_hold=abuse_hold,
            changed=changed,
        )

        account_abuse_hold_action_response.additional_properties = d
        return account_abuse_hold_action_response

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
