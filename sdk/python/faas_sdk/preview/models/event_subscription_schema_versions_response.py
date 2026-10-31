from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventSubscriptionSchemaVersionsResponse")


@_attrs_define
class EventSubscriptionSchemaVersionsResponse:
    """Current exact schema-version selection associated with one subscription."""

    subscription_id: UUID
    schema_versions: list[str]
    """Schema versions in the event subscription schema versions response: exact case-sensitive schema versions.
    Empty or omitted accepts all versions; a nonempty selection excludes unversioned events. Selection is captured
    at publication or backfill creation."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        schema_versions = self.schema_versions

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "schema_versions": schema_versions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        schema_versions = cast(list[str], d.pop("schema_versions"))

        event_subscription_schema_versions_response = cls(
            subscription_id=subscription_id,
            schema_versions=schema_versions,
        )

        return event_subscription_schema_versions_response
