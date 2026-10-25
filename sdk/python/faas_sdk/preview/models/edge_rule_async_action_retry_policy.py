from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleAsyncActionRetryPolicy")


@_attrs_define
class EdgeRuleAsyncActionRetryPolicy:
    """Optional per-route retry override; attempts are capped by the account plan."""

    max_attempts: int | Unset = UNSET
    """Total attempts including the first; 0 inherits the plan default."""
    base_seconds: float | Unset = UNSET
    """Base delay for exponential retry backoff."""
    max_seconds: float | Unset = UNSET
    """Maximum exponential backoff delay; must be at least base_seconds when both are positive."""
    jitter_seconds: float | Unset = UNSET
    """Symmetric retry jitter fraction."""

    def to_dict(self) -> dict[str, Any]:
        max_attempts = self.max_attempts

        base_seconds = self.base_seconds

        max_seconds = self.max_seconds

        jitter_seconds = self.jitter_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if max_attempts is not UNSET:
            field_dict["max_attempts"] = max_attempts
        if base_seconds is not UNSET:
            field_dict["base_seconds"] = base_seconds
        if max_seconds is not UNSET:
            field_dict["max_seconds"] = max_seconds
        if jitter_seconds is not UNSET:
            field_dict["jitter_seconds"] = jitter_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_attempts = d.pop("max_attempts", UNSET)

        base_seconds = d.pop("base_seconds", UNSET)

        max_seconds = d.pop("max_seconds", UNSET)

        jitter_seconds = d.pop("jitter_seconds", UNSET)

        edge_rule_async_action_retry_policy = cls(
            max_attempts=max_attempts,
            base_seconds=base_seconds,
            max_seconds=max_seconds,
            jitter_seconds=jitter_seconds,
        )

        return edge_rule_async_action_retry_policy
