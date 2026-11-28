from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DevSessionSummary")


@_attrs_define
class DevSessionSummary:
    """One developer environment in the account list."""

    project: str
    app: str
    """Backing dev-* app slug; DELETE /v1/dev/sessions/{project}?app= stops it."""
    url: str
    expires_at: datetime.datetime
    state: str | Unset = UNSET
    """Lease state: open, stale, tearing_down or torn_down."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project = self.project

        app = self.app

        url = self.url

        expires_at = self.expires_at.isoformat()

        state = self.state

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project": project,
                "app": app,
                "url": url,
                "expires_at": expires_at,
            }
        )
        if state is not UNSET:
            field_dict["state"] = state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        project = d.pop("project")

        app = d.pop("app")

        url = d.pop("url")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        state = d.pop("state", UNSET)

        dev_session_summary = cls(
            project=project,
            app=app,
            url=url,
            expires_at=expires_at,
            state=state,
        )

        dev_session_summary.additional_properties = d
        return dev_session_summary

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
