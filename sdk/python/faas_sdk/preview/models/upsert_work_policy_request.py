from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.upsert_work_policy_request_max_running_per_key import (
    UpsertWorkPolicyRequestMaxRunningPerKey,
    check_upsert_work_policy_request_max_running_per_key,
)
from ..models.upsert_work_policy_request_pending_updates import (
    UpsertWorkPolicyRequestPendingUpdates,
    check_upsert_work_policy_request_pending_updates,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="UpsertWorkPolicyRequest")


@_attrs_define
class UpsertWorkPolicyRequest:
    """App work policy settings; durations use whole milliseconds."""

    max_running_per_key: UpsertWorkPolicyRequestMaxRunningPerKey
    max_running_per_fairness_key: int | Unset = 0
    pending_updates: UpsertWorkPolicyRequestPendingUpdates | Unset = "all"
    debounce_ms: int | Unset = 0
    expires_after_ms: int | Unset = 0

    def to_dict(self) -> dict[str, Any]:
        max_running_per_key: int = self.max_running_per_key

        max_running_per_fairness_key = self.max_running_per_fairness_key

        pending_updates: str | Unset = UNSET
        if not isinstance(self.pending_updates, Unset):
            pending_updates = self.pending_updates

        debounce_ms = self.debounce_ms

        expires_after_ms = self.expires_after_ms

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "max_running_per_key": max_running_per_key,
            }
        )
        if max_running_per_fairness_key is not UNSET:
            field_dict["max_running_per_fairness_key"] = max_running_per_fairness_key
        if pending_updates is not UNSET:
            field_dict["pending_updates"] = pending_updates
        if debounce_ms is not UNSET:
            field_dict["debounce_ms"] = debounce_ms
        if expires_after_ms is not UNSET:
            field_dict["expires_after_ms"] = expires_after_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_running_per_key = check_upsert_work_policy_request_max_running_per_key(d.pop("max_running_per_key"))

        max_running_per_fairness_key = d.pop("max_running_per_fairness_key", UNSET)

        _pending_updates = d.pop("pending_updates", UNSET)
        pending_updates: UpsertWorkPolicyRequestPendingUpdates | Unset
        if isinstance(_pending_updates, Unset):
            pending_updates = UNSET
        else:
            pending_updates = check_upsert_work_policy_request_pending_updates(_pending_updates)

        debounce_ms = d.pop("debounce_ms", UNSET)

        expires_after_ms = d.pop("expires_after_ms", UNSET)

        upsert_work_policy_request = cls(
            max_running_per_key=max_running_per_key,
            max_running_per_fairness_key=max_running_per_fairness_key,
            pending_updates=pending_updates,
            debounce_ms=debounce_ms,
            expires_after_ms=expires_after_ms,
        )

        return upsert_work_policy_request
