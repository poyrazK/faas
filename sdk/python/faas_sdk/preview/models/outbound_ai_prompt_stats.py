from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_prompt_version_stats import OutboundAIPromptVersionStats


T = TypeVar("T", bound="OutboundAIPromptStats")


@_attrs_define
class OutboundAIPromptStats:
    """A managed AI prompt's per-version stats."""

    window_start: datetime.datetime
    live_version: int | None
    canary_version: int | None
    versions: list[OutboundAIPromptVersionStats]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        window_start = self.window_start.isoformat()

        live_version: int | None
        live_version = self.live_version

        canary_version: int | None
        canary_version = self.canary_version

        versions = []
        for versions_item_data in self.versions:
            versions_item = versions_item_data.to_dict()
            versions.append(versions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window_start": window_start,
                "live_version": live_version,
                "canary_version": canary_version,
                "versions": versions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_prompt_version_stats import OutboundAIPromptVersionStats

        d = dict(src_dict)
        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        def _parse_live_version(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        live_version = _parse_live_version(d.pop("live_version"))

        def _parse_canary_version(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        canary_version = _parse_canary_version(d.pop("canary_version"))

        versions = []
        _versions = d.pop("versions")
        for versions_item_data in _versions:
            versions_item = OutboundAIPromptVersionStats.from_dict(versions_item_data)

            versions.append(versions_item)

        outbound_ai_prompt_stats = cls(
            window_start=window_start,
            live_version=live_version,
            canary_version=canary_version,
            versions=versions,
        )

        outbound_ai_prompt_stats.additional_properties = d
        return outbound_ai_prompt_stats

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
