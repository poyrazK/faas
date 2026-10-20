from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectLifecycleNoncurrentExpiration")


@_attrs_define
class ObjectLifecycleNoncurrentExpiration:
    """Expire noncurrent versions after their successor age and optional retained-version count both qualify."""

    noncurrent_days: int
    newer_noncurrent_versions: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        noncurrent_days = self.noncurrent_days

        newer_noncurrent_versions = self.newer_noncurrent_versions

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "noncurrent_days": noncurrent_days,
            }
        )
        if newer_noncurrent_versions is not UNSET:
            field_dict["newer_noncurrent_versions"] = newer_noncurrent_versions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        noncurrent_days = d.pop("noncurrent_days")

        newer_noncurrent_versions = d.pop("newer_noncurrent_versions", UNSET)

        object_lifecycle_noncurrent_expiration = cls(
            noncurrent_days=noncurrent_days,
            newer_noncurrent_versions=newer_noncurrent_versions,
        )

        return object_lifecycle_noncurrent_expiration
