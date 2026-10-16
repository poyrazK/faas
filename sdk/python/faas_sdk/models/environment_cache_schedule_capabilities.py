from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EnvironmentCacheScheduleCapabilities")


@_attrs_define
class EnvironmentCacheScheduleCapabilities:
    """Serving API and state backend support for named-environment schedule reads. These signals do not establish target
    authorization, migration health, worker readiness or compatibility of other replicas. Upgrade all schedule writers
    before relying on activity completeness.

    """

    listing: bool
    """Supports bounded current schedule discovery."""
    activity: bool
    """Supports contiguous scoped schedule activity pages and cursor expiry."""
    checkpoint: bool
    """Supports capturing scoped feed bounds without scanning activity."""
    retention_seconds: int
    """Advertised activity retention window in seconds; zero means retention support is unavailable. Guided MCP
    recovery requires the documented 30-day contract."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        listing = self.listing

        activity = self.activity

        checkpoint = self.checkpoint

        retention_seconds = self.retention_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "listing": listing,
                "activity": activity,
                "checkpoint": checkpoint,
                "retention_seconds": retention_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        listing = d.pop("listing")

        activity = d.pop("activity")

        checkpoint = d.pop("checkpoint")

        retention_seconds = d.pop("retention_seconds")

        environment_cache_schedule_capabilities = cls(
            listing=listing,
            activity=activity,
            checkpoint=checkpoint,
            retention_seconds=retention_seconds,
        )

        environment_cache_schedule_capabilities.additional_properties = d
        return environment_cache_schedule_capabilities

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
