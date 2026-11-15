from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipelineWALRetention")


@_attrs_define
class DataPipelineWALRetention:
    """Fresh source-specific WAL headroom. The remaining-time estimate uses sampled cluster WAL generation and does not
    guarantee when a slot will be invalidated.

    """

    limit_bytes: int
    remaining_bytes: int
    estimated_seconds_remaining: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        limit_bytes = self.limit_bytes

        remaining_bytes = self.remaining_bytes

        estimated_seconds_remaining = self.estimated_seconds_remaining

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "limit_bytes": limit_bytes,
                "remaining_bytes": remaining_bytes,
            }
        )
        if estimated_seconds_remaining is not UNSET:
            field_dict["estimated_seconds_remaining"] = estimated_seconds_remaining

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        limit_bytes = d.pop("limit_bytes")

        remaining_bytes = d.pop("remaining_bytes")

        estimated_seconds_remaining = d.pop("estimated_seconds_remaining", UNSET)

        data_pipeline_wal_retention = cls(
            limit_bytes=limit_bytes,
            remaining_bytes=remaining_bytes,
            estimated_seconds_remaining=estimated_seconds_remaining,
        )

        return data_pipeline_wal_retention
