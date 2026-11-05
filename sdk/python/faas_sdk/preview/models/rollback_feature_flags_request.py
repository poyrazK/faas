from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RollbackFeatureFlagsRequest")


@_attrs_define
class RollbackFeatureFlagsRequest:
    """Publish the selected historical version while checking current version."""

    expected_version: int
    version: int

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        version = d.pop("version")

        rollback_feature_flags_request = cls(
            expected_version=expected_version,
            version=version,
        )

        return rollback_feature_flags_request
