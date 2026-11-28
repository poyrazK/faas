from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_health_signal_kind import AccountHealthSignalKind, check_account_health_signal_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountHealthSignal")


@_attrs_define
class AccountHealthSignal:
    """A problem that keeps recurring, in customer wording, with the next step when there is one."""

    kind: AccountHealthSignalKind
    """Stable signal identifier."""
    subject: str
    """What the signal is about: an app slug, a scheduled job name or a problem code."""
    title: str
    """Short headline for the signal."""
    detail: str
    """What keeps happening and how to fix it."""
    since: datetime.datetime
    """When Gregale first detected the problem."""
    action: str | Unset = UNSET
    """Label of the link that fixes the signal."""
    action_url: str | Unset = UNSET
    """Dashboard path of the page that fixes the signal."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        subject = self.subject

        title = self.title

        detail = self.detail

        since = self.since.isoformat()

        action = self.action

        action_url = self.action_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "subject": subject,
                "title": title,
                "detail": detail,
                "since": since,
            }
        )
        if action is not UNSET:
            field_dict["action"] = action
        if action_url is not UNSET:
            field_dict["action_url"] = action_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_account_health_signal_kind(d.pop("kind"))

        subject = d.pop("subject")

        title = d.pop("title")

        detail = d.pop("detail")

        since = datetime.datetime.fromisoformat(d.pop("since"))

        action = d.pop("action", UNSET)

        action_url = d.pop("action_url", UNSET)

        account_health_signal = cls(
            kind=kind,
            subject=subject,
            title=title,
            detail=detail,
            since=since,
            action=action,
            action_url=action_url,
        )

        account_health_signal.additional_properties = d
        return account_health_signal

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
