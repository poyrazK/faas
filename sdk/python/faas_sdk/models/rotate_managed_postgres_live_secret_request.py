from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="RotateManagedPostgresLiveSecretRequest")


@_attrs_define
class RotateManagedPostgresLiveSecretRequest:
    """Live secret rotation. An empty body keeps the previous secret for 24 hours."""

    overlap_seconds: int | Unset = UNSET
    """How long the previous secret keeps opening seals; 0 revokes it at once."""

    def to_dict(self) -> dict[str, Any]:
        overlap_seconds = self.overlap_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if overlap_seconds is not UNSET:
            field_dict["overlap_seconds"] = overlap_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        overlap_seconds = d.pop("overlap_seconds", UNSET)

        rotate_managed_postgres_live_secret_request = cls(
            overlap_seconds=overlap_seconds,
        )

        return rotate_managed_postgres_live_secret_request
