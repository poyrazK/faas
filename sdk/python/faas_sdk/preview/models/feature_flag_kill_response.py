from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="FeatureFlagKillResponse")


@_attrs_define
class FeatureFlagKillResponse:
    """A killed flag and the configuration version that holds the kill."""

    key: str
    killed: bool
    """Always true."""
    changed: bool
    """False when the flag was already killed and no configuration was published."""
    version: int

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        killed = self.killed

        changed = self.changed

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "killed": killed,
                "changed": changed,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        killed = d.pop("killed")

        changed = d.pop("changed")

        version = d.pop("version")

        feature_flag_kill_response = cls(
            key=key,
            killed=killed,
            changed=changed,
            version=version,
        )

        return feature_flag_kill_response
