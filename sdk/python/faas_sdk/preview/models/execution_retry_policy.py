from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionRetryPolicy")


@_attrs_define
class ExecutionRetryPolicy:
    """Multiple attempts require safe_to_repeat=true. Only restore_failed, execution_transport_failed and lease_expired are
    retried. Each attempt has a separate Run and budget reservation; external side effects may repeat.

    """

    max_attempts: int
    backoff_ms: int | Unset = 1000
    safe_to_repeat: bool | Unset = False

    def to_dict(self) -> dict[str, Any]:
        max_attempts = self.max_attempts

        backoff_ms = self.backoff_ms

        safe_to_repeat = self.safe_to_repeat

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "max_attempts": max_attempts,
            }
        )
        if backoff_ms is not UNSET:
            field_dict["backoff_ms"] = backoff_ms
        if safe_to_repeat is not UNSET:
            field_dict["safe_to_repeat"] = safe_to_repeat

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_attempts = d.pop("max_attempts")

        backoff_ms = d.pop("backoff_ms", UNSET)

        safe_to_repeat = d.pop("safe_to_repeat", UNSET)

        execution_retry_policy = cls(
            max_attempts=max_attempts,
            backoff_ms=backoff_ms,
            safe_to_repeat=safe_to_repeat,
        )

        return execution_retry_policy
