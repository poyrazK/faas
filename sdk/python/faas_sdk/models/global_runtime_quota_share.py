from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="GlobalRuntimeQuotaShare")


@_attrs_define
class GlobalRuntimeQuotaShare:
    """One selected region and its positive steady concurrency reservation."""

    region: str
    concurrency: int
    """Positive steady concurrency share from the tenant plan."""

    def to_dict(self) -> dict[str, Any]:
        region = self.region

        concurrency = self.concurrency

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "region": region,
                "concurrency": concurrency,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        region = d.pop("region")

        concurrency = d.pop("concurrency")

        global_runtime_quota_share = cls(
            region=region,
            concurrency=concurrency,
        )

        return global_runtime_quota_share
