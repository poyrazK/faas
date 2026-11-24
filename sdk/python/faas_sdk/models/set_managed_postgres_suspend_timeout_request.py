from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="SetManagedPostgresSuspendTimeoutRequest")


@_attrs_define
class SetManagedPostgresSuspendTimeoutRequest:
    """How long a scale-to-zero database stays up after its last activity before it suspends."""

    seconds: int | None
    """Seconds of inactivity before the database suspends; null returns it to the platform default."""

    def to_dict(self) -> dict[str, Any]:
        seconds: int | None
        seconds = self.seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "seconds": seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_seconds(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        seconds = _parse_seconds(d.pop("seconds"))

        set_managed_postgres_suspend_timeout_request = cls(
            seconds=seconds,
        )

        return set_managed_postgres_suspend_timeout_request
