from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.work_policy_response_max_running_per_key import (
    WorkPolicyResponseMaxRunningPerKey,
    check_work_policy_response_max_running_per_key,
)
from ..models.work_policy_response_pending_updates import (
    WorkPolicyResponsePendingUpdates,
    check_work_policy_response_pending_updates,
)

T = TypeVar("T", bound="WorkPolicyResponse")


@_attrs_define
class WorkPolicyResponse:
    """Saved app policy and revision used for subsequent work admissions."""

    name: str
    revision: int
    max_running_per_key: WorkPolicyResponseMaxRunningPerKey
    max_running_per_fairness_key: int
    pending_updates: WorkPolicyResponsePendingUpdates
    debounce_ms: int
    expires_after_ms: int
    created_at: datetime.datetime
    updated_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        revision = self.revision

        max_running_per_key: int = self.max_running_per_key

        max_running_per_fairness_key = self.max_running_per_fairness_key

        pending_updates: str = self.pending_updates

        debounce_ms = self.debounce_ms

        expires_after_ms = self.expires_after_ms

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "revision": revision,
                "max_running_per_key": max_running_per_key,
                "max_running_per_fairness_key": max_running_per_fairness_key,
                "pending_updates": pending_updates,
                "debounce_ms": debounce_ms,
                "expires_after_ms": expires_after_ms,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        revision = d.pop("revision")

        max_running_per_key = check_work_policy_response_max_running_per_key(d.pop("max_running_per_key"))

        max_running_per_fairness_key = d.pop("max_running_per_fairness_key")

        pending_updates = check_work_policy_response_pending_updates(d.pop("pending_updates"))

        debounce_ms = d.pop("debounce_ms")

        expires_after_ms = d.pop("expires_after_ms")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        work_policy_response = cls(
            name=name,
            revision=revision,
            max_running_per_key=max_running_per_key,
            max_running_per_fairness_key=max_running_per_fairness_key,
            pending_updates=pending_updates,
            debounce_ms=debounce_ms,
            expires_after_ms=expires_after_ms,
            created_at=created_at,
            updated_at=updated_at,
        )

        return work_policy_response
