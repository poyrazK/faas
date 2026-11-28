from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="TaskReviewLimits")


@_attrs_define
class TaskReviewLimits:
    """Explicit configured Task overrides only; no binding or readiness evidence."""

    ttl_seconds: int | Unset = UNSET
    worker_concurrency: int | Unset = UNSET
    max_attempts: int | Unset = UNSET
    max_running: int | Unset = UNSET
    max_running_per_owner: int | Unset = UNSET
    max_outstanding: int | Unset = UNSET
    max_outstanding_per_owner: int | Unset = UNSET
    shutdown_timeout_ms: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        ttl_seconds = self.ttl_seconds

        worker_concurrency = self.worker_concurrency

        max_attempts = self.max_attempts

        max_running = self.max_running

        max_running_per_owner = self.max_running_per_owner

        max_outstanding = self.max_outstanding

        max_outstanding_per_owner = self.max_outstanding_per_owner

        shutdown_timeout_ms = self.shutdown_timeout_ms

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds
        if worker_concurrency is not UNSET:
            field_dict["worker_concurrency"] = worker_concurrency
        if max_attempts is not UNSET:
            field_dict["max_attempts"] = max_attempts
        if max_running is not UNSET:
            field_dict["max_running"] = max_running
        if max_running_per_owner is not UNSET:
            field_dict["max_running_per_owner"] = max_running_per_owner
        if max_outstanding is not UNSET:
            field_dict["max_outstanding"] = max_outstanding
        if max_outstanding_per_owner is not UNSET:
            field_dict["max_outstanding_per_owner"] = max_outstanding_per_owner
        if shutdown_timeout_ms is not UNSET:
            field_dict["shutdown_timeout_ms"] = shutdown_timeout_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ttl_seconds = d.pop("ttl_seconds", UNSET)

        worker_concurrency = d.pop("worker_concurrency", UNSET)

        max_attempts = d.pop("max_attempts", UNSET)

        max_running = d.pop("max_running", UNSET)

        max_running_per_owner = d.pop("max_running_per_owner", UNSET)

        max_outstanding = d.pop("max_outstanding", UNSET)

        max_outstanding_per_owner = d.pop("max_outstanding_per_owner", UNSET)

        shutdown_timeout_ms = d.pop("shutdown_timeout_ms", UNSET)

        task_review_limits = cls(
            ttl_seconds=ttl_seconds,
            worker_concurrency=worker_concurrency,
            max_attempts=max_attempts,
            max_running=max_running,
            max_running_per_owner=max_running_per_owner,
            max_outstanding=max_outstanding,
            max_outstanding_per_owner=max_outstanding_per_owner,
            shutdown_timeout_ms=shutdown_timeout_ms,
        )

        return task_review_limits
