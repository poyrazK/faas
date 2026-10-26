from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="StorageUsageResponse")


@_attrs_define
class StorageUsageResponse:
    """Per-app storage rollup for one calendar day (informational; not billed today). Mirrors the `snapshot_storage_daily`
    table populated by the meterd storage rollup loop (ADR-049 §B.3, migration 00070).

    """

    app_id: str
    day: str
    snapshot_bytes: int
    """Cumulative snapshot bytes (mem_bytes + disk_bytes) for the day (informational; not billed today). ADR-049
    §B.3."""
    layer_bytes: int
    """Cumulative overlay-staging layer bytes for the day (informational; not billed today). ADR-049 §B.3."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        day = self.day

        snapshot_bytes = self.snapshot_bytes

        layer_bytes = self.layer_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "day": day,
                "snapshot_bytes": snapshot_bytes,
                "layer_bytes": layer_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        day = d.pop("day")

        snapshot_bytes = d.pop("snapshot_bytes")

        layer_bytes = d.pop("layer_bytes")

        storage_usage_response = cls(
            app_id=app_id,
            day=day,
            snapshot_bytes=snapshot_bytes,
            layer_bytes=layer_bytes,
        )

        storage_usage_response.additional_properties = d
        return storage_usage_response

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
