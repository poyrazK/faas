from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.flags_config import FlagsConfig


T = TypeVar("T", bound="UpdateFeatureFlagsRequest")


@_attrs_define
class UpdateFeatureFlagsRequest:
    """Optimistic atomic configuration publication."""

    expected_version: int
    config: FlagsConfig
    """Complete atomic environment configuration; omitted flags are removed from current evaluation."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        config = self.config.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "config": config,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flags_config import FlagsConfig

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        config = FlagsConfig.from_dict(d.pop("config"))

        update_feature_flags_request = cls(
            expected_version=expected_version,
            config=config,
        )

        return update_feature_flags_request
