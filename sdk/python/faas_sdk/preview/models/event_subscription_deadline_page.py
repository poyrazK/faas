from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_subscription_deadline_page_coverage import (
    EventSubscriptionDeadlinePageCoverage,
    check_event_subscription_deadline_page_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_subscription_deadline_recipient import EventSubscriptionDeadlineRecipient


T = TypeVar("T", bound="EventSubscriptionDeadlinePage")


@_attrs_define
class EventSubscriptionDeadlinePage:
    """One read-only live page ordered by effective deadline, acceptance time and internal receipt identity. No aggregate
    health or frozen membership is implied. Empty recipients are not proof of successful delivery.

    """

    coverage: EventSubscriptionDeadlinePageCoverage
    app_id: UUID
    subscription_id: UUID
    observed_at: datetime.datetime
    window_at: datetime.datetime
    read_only: bool
    recipients: list[EventSubscriptionDeadlineRecipient]
    has_more: bool
    """A lookahead row existed in this page snapshot; later pages can still be empty as routing state changes."""
    next_cursor: str | Unset = UNSET
    """Present only when has_more is true. Carries no authorization."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        coverage: str = self.coverage

        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        observed_at = self.observed_at.isoformat()

        window_at = self.window_at.isoformat()

        read_only = self.read_only

        recipients = []
        for recipients_item_data in self.recipients:
            recipients_item = recipients_item_data.to_dict()
            recipients.append(recipients_item)

        has_more = self.has_more

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "coverage": coverage,
                "app_id": app_id,
                "subscription_id": subscription_id,
                "observed_at": observed_at,
                "window_at": window_at,
                "read_only": read_only,
                "recipients": recipients,
                "has_more": has_more,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_deadline_recipient import EventSubscriptionDeadlineRecipient

        d = dict(src_dict)
        coverage = check_event_subscription_deadline_page_coverage(d.pop("coverage"))

        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        window_at = datetime.datetime.fromisoformat(d.pop("window_at"))

        read_only = d.pop("read_only")

        recipients = []
        _recipients = d.pop("recipients")
        for recipients_item_data in _recipients:
            recipients_item = EventSubscriptionDeadlineRecipient.from_dict(recipients_item_data)

            recipients.append(recipients_item)

        has_more = d.pop("has_more")

        next_cursor = d.pop("next_cursor", UNSET)

        event_subscription_deadline_page = cls(
            coverage=coverage,
            app_id=app_id,
            subscription_id=subscription_id,
            observed_at=observed_at,
            window_at=window_at,
            read_only=read_only,
            recipients=recipients,
            has_more=has_more,
            next_cursor=next_cursor,
        )

        event_subscription_deadline_page.additional_properties = d
        return event_subscription_deadline_page

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
