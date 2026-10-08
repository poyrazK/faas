from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAppForkRequest")


@_attrs_define
class CreateAppForkRequest:
    """Body of `POST /v1/apps/{slug}/forks`. Every field is optional."""

    ttl_seconds: int | Unset = UNSET
    """How long the fork lives. Default 3600 (1 h), maximum 14400 (4 h)."""

    def to_dict(self) -> dict[str, Any]:
        ttl_seconds = self.ttl_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ttl_seconds = d.pop("ttl_seconds", UNSET)

        create_app_fork_request = cls(
            ttl_seconds=ttl_seconds,
        )

        return create_app_fork_request
