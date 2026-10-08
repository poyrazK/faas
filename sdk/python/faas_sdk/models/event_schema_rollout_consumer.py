from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventSchemaRolloutConsumer")


@_attrs_define
class EventSchemaRolloutConsumer:
    subscription_id: UUID
    app_id: UUID
    source: str
    """Subscription source pattern."""
    type_: str
    """Subscription type pattern."""
    schema_versions: list[str]
    """Empty accepts every version."""
    accepts_version: bool
    """Version selection only; no content filter evaluation or delivery guarantee."""
    content_filter_present: bool

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        source = self.source

        type_ = self.type_

        schema_versions = self.schema_versions

        accepts_version = self.accepts_version

        content_filter_present = self.content_filter_present

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "app_id": app_id,
                "source": source,
                "type": type_,
                "schema_versions": schema_versions,
                "accepts_version": accepts_version,
                "content_filter_present": content_filter_present,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        source = d.pop("source")

        type_ = d.pop("type")

        schema_versions = cast(list[str], d.pop("schema_versions"))

        accepts_version = d.pop("accepts_version")

        content_filter_present = d.pop("content_filter_present")

        event_schema_rollout_consumer = cls(
            subscription_id=subscription_id,
            app_id=app_id,
            source=source,
            type_=type_,
            schema_versions=schema_versions,
            accepts_version=accepts_version,
            content_filter_present=content_filter_present,
        )

        return event_schema_rollout_consumer
