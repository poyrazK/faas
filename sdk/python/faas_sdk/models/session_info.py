from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="SessionInfo")


@_attrs_define
class SessionInfo:
    """One row per dashboard login (ADR-039 / IAM-3). The
    cookie envelope carries the row's `id` as `sid`;
    every authenticated request re-validates the row.
    Revoked rows are filtered out of the
    `GET /v1/auth/sessions` response.

    """

    id: UUID
    """Session id. Also stamped as `sid` in the cookie envelope."""
    account_id: UUID
    issued_at: datetime.datetime
    """When the session was minted (RFC 3339, UTC)."""
    current_session: bool
    """True when this row's id matches the calling cookie's sid. Exactly one row per list response has this flag
    set."""
    issued_ip: str | Unset = UNSET
    """Client IP captured at login (host portion only; IPv4 or IPv6). Empty when the request's RemoteAddr was
    unparseable."""
    issued_ua: str | Unset = UNSET
    """User-Agent header captured at login. May be empty when the client suppressed the header."""
    last_seen_at: datetime.datetime | Unset = UNSET
    """Last time the cookie cross-validated this row. Updated with a 5-minute debounce; continues post-revoke
    (operational signal only, not authorization)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = str(self.account_id)

        issued_at = self.issued_at.isoformat()

        current_session = self.current_session

        issued_ip = self.issued_ip

        issued_ua = self.issued_ua

        last_seen_at: str | Unset = UNSET
        if not isinstance(self.last_seen_at, Unset):
            last_seen_at = self.last_seen_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "issued_at": issued_at,
                "current_session": current_session,
            }
        )
        if issued_ip is not UNSET:
            field_dict["issued_ip"] = issued_ip
        if issued_ua is not UNSET:
            field_dict["issued_ua"] = issued_ua
        if last_seen_at is not UNSET:
            field_dict["last_seen_at"] = last_seen_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        account_id = UUID(d.pop("account_id"))

        issued_at = datetime.datetime.fromisoformat(d.pop("issued_at"))

        current_session = d.pop("current_session")

        issued_ip = d.pop("issued_ip", UNSET)

        issued_ua = d.pop("issued_ua", UNSET)

        _last_seen_at = d.pop("last_seen_at", UNSET)
        last_seen_at: datetime.datetime | Unset
        if isinstance(_last_seen_at, Unset):
            last_seen_at = UNSET
        else:
            last_seen_at = datetime.datetime.fromisoformat(_last_seen_at)

        session_info = cls(
            id=id,
            account_id=account_id,
            issued_at=issued_at,
            current_session=current_session,
            issued_ip=issued_ip,
            issued_ua=issued_ua,
            last_seen_at=last_seen_at,
        )

        session_info.additional_properties = d
        return session_info

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
