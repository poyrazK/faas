from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_api_change_hook_status_publishing import (
    DataAPIChangeHookStatusPublishing,
    check_data_api_change_hook_status_publishing,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DataAPIChangeHookStatus")


@_attrs_define
class DataAPIChangeHookStatus:
    """The change hook delivery the app's runtime last reported."""

    reported_at: datetime.datetime
    """When the platform received the report."""
    pending: int
    """Recorded changes waiting to publish."""
    failed: int
    """Changes the platform rejected, kept until retried or discarded in SQL."""
    publishing: DataAPIChangeHookStatusPublishing
    """retrying when waiting changes are held back by publications that keep failing."""
    oldest_pending_at: datetime.datetime | Unset = UNSET
    """When the oldest waiting change was recorded."""
    last_published_at: datetime.datetime | Unset = UNSET
    """When a runtime last published changes."""
    last_failure_code: str | Unset = UNSET
    """The code of the latest rejected change."""
    last_dropped_at: datetime.datetime | Unset = UNSET
    """When capture last left a change out because the backlog was full; those changes were not recorded."""

    def to_dict(self) -> dict[str, Any]:
        reported_at = self.reported_at.isoformat()

        pending = self.pending

        failed = self.failed

        publishing: str = self.publishing

        oldest_pending_at: str | Unset = UNSET
        if not isinstance(self.oldest_pending_at, Unset):
            oldest_pending_at = self.oldest_pending_at.isoformat()

        last_published_at: str | Unset = UNSET
        if not isinstance(self.last_published_at, Unset):
            last_published_at = self.last_published_at.isoformat()

        last_failure_code = self.last_failure_code

        last_dropped_at: str | Unset = UNSET
        if not isinstance(self.last_dropped_at, Unset):
            last_dropped_at = self.last_dropped_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "reported_at": reported_at,
                "pending": pending,
                "failed": failed,
                "publishing": publishing,
            }
        )
        if oldest_pending_at is not UNSET:
            field_dict["oldest_pending_at"] = oldest_pending_at
        if last_published_at is not UNSET:
            field_dict["last_published_at"] = last_published_at
        if last_failure_code is not UNSET:
            field_dict["last_failure_code"] = last_failure_code
        if last_dropped_at is not UNSET:
            field_dict["last_dropped_at"] = last_dropped_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reported_at = datetime.datetime.fromisoformat(d.pop("reported_at"))

        pending = d.pop("pending")

        failed = d.pop("failed")

        publishing = check_data_api_change_hook_status_publishing(d.pop("publishing"))

        _oldest_pending_at = d.pop("oldest_pending_at", UNSET)
        oldest_pending_at: datetime.datetime | Unset
        if isinstance(_oldest_pending_at, Unset):
            oldest_pending_at = UNSET
        else:
            oldest_pending_at = datetime.datetime.fromisoformat(_oldest_pending_at)

        _last_published_at = d.pop("last_published_at", UNSET)
        last_published_at: datetime.datetime | Unset
        if isinstance(_last_published_at, Unset):
            last_published_at = UNSET
        else:
            last_published_at = datetime.datetime.fromisoformat(_last_published_at)

        last_failure_code = d.pop("last_failure_code", UNSET)

        _last_dropped_at = d.pop("last_dropped_at", UNSET)
        last_dropped_at: datetime.datetime | Unset
        if isinstance(_last_dropped_at, Unset):
            last_dropped_at = UNSET
        else:
            last_dropped_at = datetime.datetime.fromisoformat(_last_dropped_at)

        data_api_change_hook_status = cls(
            reported_at=reported_at,
            pending=pending,
            failed=failed,
            publishing=publishing,
            oldest_pending_at=oldest_pending_at,
            last_published_at=last_published_at,
            last_failure_code=last_failure_code,
            last_dropped_at=last_dropped_at,
        )

        return data_api_change_hook_status
