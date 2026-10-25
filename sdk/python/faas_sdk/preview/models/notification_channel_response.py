from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.notification_channel_response_kind import (
    NotificationChannelResponseKind,
    check_notification_channel_response_kind,
)
from ..models.notification_channel_response_pagerduty_region import (
    NotificationChannelResponsePagerdutyRegion,
    check_notification_channel_response_pagerduty_region,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="NotificationChannelResponse")


@_attrs_define
class NotificationChannelResponse:
    """One alert notification channel (ADR-749)."""

    id: UUID
    name: str
    kind: NotificationChannelResponseKind
    target: str
    """Non-secret hint: Slack workspace and hook ids, the routing key last four characters, or the email address."""
    created_at: datetime.datetime
    pagerduty_region: NotificationChannelResponsePagerdutyRegion | Unset = UNSET
    """Region of a PagerDuty channel."""
    last_delivered_at: datetime.datetime | Unset = UNSET
    """Last successful delivery or test."""
    last_error: str | Unset = UNSET
    """Most recent delivery error, if any."""
    last_error_at: datetime.datetime | Unset = UNSET
    """When last_error occurred."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        kind: str = self.kind

        target = self.target

        created_at = self.created_at.isoformat()

        pagerduty_region: str | Unset = UNSET
        if not isinstance(self.pagerduty_region, Unset):
            pagerduty_region = self.pagerduty_region

        last_delivered_at: str | Unset = UNSET
        if not isinstance(self.last_delivered_at, Unset):
            last_delivered_at = self.last_delivered_at.isoformat()

        last_error = self.last_error

        last_error_at: str | Unset = UNSET
        if not isinstance(self.last_error_at, Unset):
            last_error_at = self.last_error_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "kind": kind,
                "target": target,
                "created_at": created_at,
            }
        )
        if pagerduty_region is not UNSET:
            field_dict["pagerduty_region"] = pagerduty_region
        if last_delivered_at is not UNSET:
            field_dict["last_delivered_at"] = last_delivered_at
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if last_error_at is not UNSET:
            field_dict["last_error_at"] = last_error_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        kind = check_notification_channel_response_kind(d.pop("kind"))

        target = d.pop("target")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _pagerduty_region = d.pop("pagerduty_region", UNSET)
        pagerduty_region: NotificationChannelResponsePagerdutyRegion | Unset
        if isinstance(_pagerduty_region, Unset):
            pagerduty_region = UNSET
        else:
            pagerduty_region = check_notification_channel_response_pagerduty_region(_pagerduty_region)

        _last_delivered_at = d.pop("last_delivered_at", UNSET)
        last_delivered_at: datetime.datetime | Unset
        if isinstance(_last_delivered_at, Unset):
            last_delivered_at = UNSET
        else:
            last_delivered_at = datetime.datetime.fromisoformat(_last_delivered_at)

        last_error = d.pop("last_error", UNSET)

        _last_error_at = d.pop("last_error_at", UNSET)
        last_error_at: datetime.datetime | Unset
        if isinstance(_last_error_at, Unset):
            last_error_at = UNSET
        else:
            last_error_at = datetime.datetime.fromisoformat(_last_error_at)

        notification_channel_response = cls(
            id=id,
            name=name,
            kind=kind,
            target=target,
            created_at=created_at,
            pagerduty_region=pagerduty_region,
            last_delivered_at=last_delivered_at,
            last_error=last_error,
            last_error_at=last_error_at,
        )

        notification_channel_response.additional_properties = d
        return notification_channel_response

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
