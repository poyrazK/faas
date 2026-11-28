from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="SecurityActivityEvent")


@_attrs_define
class SecurityActivityEvent:
    """One sign-in, credential or account-security change."""

    at: datetime.datetime
    kind: str
    """Audit event kind, e.g. auth.session.created or api_key.created."""
    summary: str
    """Fixed English phrase for the kind."""
    ip: str | Unset = UNSET
    """Client address recorded with the event."""
    detail: str | Unset = UNSET
    """Sign-in method, provider or key label, when recorded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        kind = self.kind

        summary = self.summary

        ip = self.ip

        detail = self.detail

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at": at,
                "kind": kind,
                "summary": summary,
            }
        )
        if ip is not UNSET:
            field_dict["ip"] = ip
        if detail is not UNSET:
            field_dict["detail"] = detail

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        kind = d.pop("kind")

        summary = d.pop("summary")

        ip = d.pop("ip", UNSET)

        detail = d.pop("detail", UNSET)

        security_activity_event = cls(
            at=at,
            kind=kind,
            summary=summary,
            ip=ip,
            detail=detail,
        )

        security_activity_event.additional_properties = d
        return security_activity_event

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
