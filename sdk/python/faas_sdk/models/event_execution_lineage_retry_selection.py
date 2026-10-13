from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventExecutionLineageRetrySelection")


@_attrs_define
class EventExecutionLineageRetrySelection:
    outbox_id: int
    expected_revision: int
    accepted_at: datetime.datetime
    """Exact original acceptance timestamp from the reviewed case."""

    def to_dict(self) -> dict[str, Any]:
        outbox_id = self.outbox_id

        expected_revision = self.expected_revision

        accepted_at = self.accepted_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outbox_id": outbox_id,
                "expected_revision": expected_revision,
                "accepted_at": accepted_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        outbox_id = d.pop("outbox_id")

        expected_revision = d.pop("expected_revision")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        event_execution_lineage_retry_selection = cls(
            outbox_id=outbox_id,
            expected_revision=expected_revision,
            accepted_at=accepted_at,
        )

        return event_execution_lineage_retry_selection
