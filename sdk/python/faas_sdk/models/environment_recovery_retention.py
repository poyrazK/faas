from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EnvironmentRecoveryRetention")


@_attrs_define
class EnvironmentRecoveryRetention:
    """Explicit artifact retention; count, age and byte limits apply together. Removing the policy or detaching releases
    its protection. No snapshot or trust transfer.

    """

    releases: int
    """Maximum newest completed serving releases, including the active release when served."""
    seconds: int
    """Maximum age measured from original serving completion; observations never renew it."""
    max_bytes: int
    """Conservative per-release unique layer bytes budget; account policy reservations are limited to 2147483648
    bytes."""

    def to_dict(self) -> dict[str, Any]:
        releases = self.releases

        seconds = self.seconds

        max_bytes = self.max_bytes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "releases": releases,
                "seconds": seconds,
                "max_bytes": max_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        releases = d.pop("releases")

        seconds = d.pop("seconds")

        max_bytes = d.pop("max_bytes")

        environment_recovery_retention = cls(
            releases=releases,
            seconds=seconds,
            max_bytes=max_bytes,
        )

        return environment_recovery_retention
