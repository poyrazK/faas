from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_change_event_source import AppChangeEventSource, check_app_change_event_source
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppChangeEvent")


@_attrs_define
class AppChangeEvent:
    """One change timeline entry with a platform-generated summary."""

    at: datetime.datetime
    source: AppChangeEventSource
    kind: str
    """Source-specific kind such as deploy.rolled_back or env.set."""
    summary: str
    deployment_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        source: str = self.source

        kind = self.kind

        summary = self.summary

        deployment_id = self.deployment_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at": at,
                "source": source,
                "kind": kind,
                "summary": summary,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        source = check_app_change_event_source(d.pop("source"))

        kind = d.pop("kind")

        summary = d.pop("summary")

        deployment_id = d.pop("deployment_id", UNSET)

        app_change_event = cls(
            at=at,
            source=source,
            kind=kind,
            summary=summary,
            deployment_id=deployment_id,
        )

        app_change_event.additional_properties = d
        return app_change_event

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
