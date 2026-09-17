from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="InvocationDestinations")


@_attrs_define
class InvocationDestinations:
    """EPIC #1278. Optional terminal callbacks for an async invocation. Values are app webhook subscription IDs owned by
    the invoking app.

    """

    on_success: UUID | Unset = UNSET
    """Webhook subscription receiving a job.finished event after completion."""
    on_failure: UUID | Unset = UNSET
    """Webhook subscription receiving a job.finished event after permanent failure or DLQ routing."""

    def to_dict(self) -> dict[str, Any]:
        on_success: str | Unset = UNSET
        if not isinstance(self.on_success, Unset):
            on_success = str(self.on_success)

        on_failure: str | Unset = UNSET
        if not isinstance(self.on_failure, Unset):
            on_failure = str(self.on_failure)

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if on_success is not UNSET:
            field_dict["on_success"] = on_success
        if on_failure is not UNSET:
            field_dict["on_failure"] = on_failure

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _on_success = d.pop("on_success", UNSET)
        on_success: UUID | Unset
        if isinstance(_on_success, Unset):
            on_success = UNSET
        else:
            on_success = UUID(_on_success)

        _on_failure = d.pop("on_failure", UNSET)
        on_failure: UUID | Unset
        if isinstance(_on_failure, Unset):
            on_failure = UNSET
        else:
            on_failure = UUID(_on_failure)

        invocation_destinations = cls(
            on_success=on_success,
            on_failure=on_failure,
        )

        return invocation_destinations
