from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventSubscriptionSchemaVersionsRequest")


@_attrs_define
class EventSubscriptionSchemaVersionsRequest:
    """Replacement schema-version selection for future subscription publications and backfills."""

    schema_versions: list[str]
    """Schema versions in the event subscription schema versions request: exact case-sensitive schema versions.
    Empty or omitted accepts all versions; a nonempty selection excludes unversioned events. Selection is captured
    at publication or backfill creation."""

    def to_dict(self) -> dict[str, Any]:
        schema_versions = self.schema_versions

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "schema_versions": schema_versions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        schema_versions = cast(list[str], d.pop("schema_versions"))

        event_subscription_schema_versions_request = cls(
            schema_versions=schema_versions,
        )

        return event_subscription_schema_versions_request
