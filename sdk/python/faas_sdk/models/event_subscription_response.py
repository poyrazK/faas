from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_subscription_response_filter import EventSubscriptionResponseFilter


T = TypeVar("T", bound="EventSubscriptionResponse")


@_attrs_define
class EventSubscriptionResponse:
    """One manifest-declared event subscription reconciled for an app."""

    id: UUID
    app_id: UUID
    source: str
    """Event source pattern, including supported wildcard forms."""
    type_: str
    """Event type pattern, including supported wildcard forms."""
    filter_: EventSubscriptionResponseFilter
    """Normalized JSON filter evaluated by the event matcher."""
    enabled: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        source = self.source

        type_ = self.type_

        filter_ = self.filter_.to_dict()

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "source": source,
                "type": type_,
                "filter": filter_,
                "enabled": enabled,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_response_filter import EventSubscriptionResponseFilter

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        source = d.pop("source")

        type_ = d.pop("type")

        filter_ = EventSubscriptionResponseFilter.from_dict(d.pop("filter"))

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        event_subscription_response = cls(
            id=id,
            app_id=app_id,
            source=source,
            type_=type_,
            filter_=filter_,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
        )

        return event_subscription_response
