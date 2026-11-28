from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_health_restriction_kind import AccountHealthRestrictionKind, check_account_health_restriction_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountHealthRestriction")


@_attrs_define
class AccountHealthRestriction:
    """One account restriction in customer wording, with the next step when there is one."""

    kind: AccountHealthRestrictionKind
    """Stable restriction identifier."""
    title: str
    """Short headline for the restriction."""
    detail: str
    """What the restriction does to the account."""
    action: str | Unset = UNSET
    """Label of the next step."""
    action_url: str | Unset = UNSET
    """Dashboard path for the next step."""
    since: datetime.datetime | Unset = UNSET
    """When the restriction started, when known."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        title = self.title

        detail = self.detail

        action = self.action

        action_url = self.action_url

        since: str | Unset = UNSET
        if not isinstance(self.since, Unset):
            since = self.since.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "title": title,
                "detail": detail,
            }
        )
        if action is not UNSET:
            field_dict["action"] = action
        if action_url is not UNSET:
            field_dict["action_url"] = action_url
        if since is not UNSET:
            field_dict["since"] = since

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_account_health_restriction_kind(d.pop("kind"))

        title = d.pop("title")

        detail = d.pop("detail")

        action = d.pop("action", UNSET)

        action_url = d.pop("action_url", UNSET)

        _since = d.pop("since", UNSET)
        since: datetime.datetime | Unset
        if isinstance(_since, Unset):
            since = UNSET
        else:
            since = datetime.datetime.fromisoformat(_since)

        account_health_restriction = cls(
            kind=kind,
            title=title,
            detail=detail,
            action=action,
            action_url=action_url,
            since=since,
        )

        account_health_restriction.additional_properties = d
        return account_health_restriction

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
