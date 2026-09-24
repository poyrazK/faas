from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_preview_subscription_filter import EventPreviewSubscriptionFilter


T = TypeVar("T", bound="EventPreviewSubscription")


@_attrs_define
class EventPreviewSubscription:
    """A bounded sample of an enabled subscription considered by the event router."""

    app_slug: str
    subscription_id: UUID
    source: str
    type_: str
    filter_: EventPreviewSubscriptionFilter
    """Normalized content filter declared by this subscription."""
    reason: str
    """would_deliver, content_filter_mismatch, pattern_mismatch, tenant_mismatch, or an invalid_subscription
    explanation."""

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        subscription_id = str(self.subscription_id)

        source = self.source

        type_ = self.type_

        filter_ = self.filter_.to_dict()

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "subscription_id": subscription_id,
                "source": source,
                "type": type_,
                "filter": filter_,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_preview_subscription_filter import EventPreviewSubscriptionFilter

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        subscription_id = UUID(d.pop("subscription_id"))

        source = d.pop("source")

        type_ = d.pop("type")

        filter_ = EventPreviewSubscriptionFilter.from_dict(d.pop("filter"))

        reason = d.pop("reason")

        event_preview_subscription = cls(
            app_slug=app_slug,
            subscription_id=subscription_id,
            source=source,
            type_=type_,
            filter_=filter_,
            reason=reason,
        )

        return event_preview_subscription
