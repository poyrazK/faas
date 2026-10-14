from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CancelEventExecutionLineageRetryJobRequest")


@_attrs_define
class CancelEventExecutionLineageRetryJobRequest:
    """Original job creation guard and optional command identity for cancelling bulk lineage retries."""

    expected_created_at: datetime.datetime
    """Exact original created_at from the reviewed retained job; fences a replacement after expiry."""
    command_id: UUID | Unset = UNSET
    """Optional nonzero command UUID; reuse with the original cancel action and creation guard after a lost
    response."""

    def to_dict(self) -> dict[str, Any]:
        expected_created_at = self.expected_created_at.isoformat()

        command_id: str | Unset = UNSET
        if not isinstance(self.command_id, Unset):
            command_id = str(self.command_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_created_at": expected_created_at,
            }
        )
        if command_id is not UNSET:
            field_dict["command_id"] = command_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_created_at = datetime.datetime.fromisoformat(d.pop("expected_created_at"))

        _command_id = d.pop("command_id", UNSET)
        command_id: UUID | Unset
        if isinstance(_command_id, Unset):
            command_id = UNSET
        else:
            command_id = UUID(_command_id)

        cancel_event_execution_lineage_retry_job_request = cls(
            expected_created_at=expected_created_at,
            command_id=command_id,
        )

        return cancel_event_execution_lineage_retry_job_request
