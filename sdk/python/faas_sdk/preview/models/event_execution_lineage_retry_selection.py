from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventExecutionLineageRetrySelection")


@_attrs_define
class EventExecutionLineageRetrySelection:
    """One reviewed lineage case with its revision and optional diagnostic incarnation guard."""

    outbox_id: int
    expected_revision: int
    accepted_at: datetime.datetime
    """Exact original acceptance timestamp from the reviewed case."""
    expected_diagnostic_id: UUID | Unset = UNSET
    """Required when the reviewed case has diagnostic_id; omit for legacy cases."""

    def to_dict(self) -> dict[str, Any]:
        outbox_id = self.outbox_id

        expected_revision = self.expected_revision

        accepted_at = self.accepted_at.isoformat()

        expected_diagnostic_id: str | Unset = UNSET
        if not isinstance(self.expected_diagnostic_id, Unset):
            expected_diagnostic_id = str(self.expected_diagnostic_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outbox_id": outbox_id,
                "expected_revision": expected_revision,
                "accepted_at": accepted_at,
            }
        )
        if expected_diagnostic_id is not UNSET:
            field_dict["expected_diagnostic_id"] = expected_diagnostic_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        outbox_id = d.pop("outbox_id")

        expected_revision = d.pop("expected_revision")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        _expected_diagnostic_id = d.pop("expected_diagnostic_id", UNSET)
        expected_diagnostic_id: UUID | Unset
        if isinstance(_expected_diagnostic_id, Unset):
            expected_diagnostic_id = UNSET
        else:
            expected_diagnostic_id = UUID(_expected_diagnostic_id)

        event_execution_lineage_retry_selection = cls(
            outbox_id=outbox_id,
            expected_revision=expected_revision,
            accepted_at=accepted_at,
            expected_diagnostic_id=expected_diagnostic_id,
        )

        return event_execution_lineage_retry_selection
