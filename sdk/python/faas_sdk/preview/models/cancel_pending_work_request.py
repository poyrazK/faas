from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="CancelPendingWorkRequest")


@_attrs_define
class CancelPendingWorkRequest:
    """Typed application key identifying pending work in one policy lane."""

    key: Any
    """A bounded JSON string, number, or boolean."""

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        cancel_pending_work_request = cls(
            key=key,
        )

        return cancel_pending_work_request
