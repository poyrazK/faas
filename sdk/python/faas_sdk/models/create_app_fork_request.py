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
    live: bool | Unset = UNSET
    """Capture the app's newest running instance now and fork that
    capture, instead of the deployment's last snapshot. The capture
    pauses the instance briefly. Refused (409 `live_fork_refused`)
    when no instance is running, a capture is in flight, or one was
    taken in the last minute.
    """

    def to_dict(self) -> dict[str, Any]:
        ttl_seconds = self.ttl_seconds

        live = self.live

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds
        if live is not UNSET:
            field_dict["live"] = live

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ttl_seconds = d.pop("ttl_seconds", UNSET)

        live = d.pop("live", UNSET)

        create_app_fork_request = cls(
            ttl_seconds=ttl_seconds,
            live=live,
        )

        return create_app_fork_request
