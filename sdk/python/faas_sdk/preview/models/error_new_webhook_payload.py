from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ErrorNewWebhookPayload")


@_attrs_define
class ErrorNewWebhookPayload:
    """First-observation details delivered with error.new."""

    app_id: str
    fingerprint: str
    route: str | Unset = UNSET
    class_: str | Unset = UNSET
    sample: str | Unset = UNSET
    first_seen_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        fingerprint = self.fingerprint

        route = self.route

        class_ = self.class_

        sample = self.sample

        first_seen_at: str | Unset = UNSET
        if not isinstance(self.first_seen_at, Unset):
            first_seen_at = self.first_seen_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "fingerprint": fingerprint,
            }
        )
        if route is not UNSET:
            field_dict["route"] = route
        if class_ is not UNSET:
            field_dict["class"] = class_
        if sample is not UNSET:
            field_dict["sample"] = sample
        if first_seen_at is not UNSET:
            field_dict["first_seen_at"] = first_seen_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        fingerprint = d.pop("fingerprint")

        route = d.pop("route", UNSET)

        class_ = d.pop("class", UNSET)

        sample = d.pop("sample", UNSET)

        _first_seen_at = d.pop("first_seen_at", UNSET)
        first_seen_at: datetime.datetime | Unset
        if isinstance(_first_seen_at, Unset):
            first_seen_at = UNSET
        else:
            first_seen_at = datetime.datetime.fromisoformat(_first_seen_at)

        error_new_webhook_payload = cls(
            app_id=app_id,
            fingerprint=fingerprint,
            route=route,
            class_=class_,
            sample=sample,
            first_seen_at=first_seen_at,
        )

        error_new_webhook_payload.additional_properties = d
        return error_new_webhook_payload

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
