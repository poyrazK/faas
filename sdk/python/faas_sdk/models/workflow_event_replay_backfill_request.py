from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="WorkflowEventReplayBackfillRequest")


@_attrs_define
class WorkflowEventReplayBackfillRequest:
    """Current event-triggered workflow and acceptance-time range for a durable historical run-admission job."""

    workflow_name: str
    """Workflow in the app's preferred live default deployment; its definition is pinned when the job is created."""
    from_: datetime.datetime
    """Inclusive lower bound on platform event acceptance."""
    until: datetime.datetime
    """Exclusive acceptance upper bound; future values stop at job creation. At most 30 days are allowed."""

    def to_dict(self) -> dict[str, Any]:
        workflow_name = self.workflow_name

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow_name": workflow_name,
                "from": from_,
                "until": until,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workflow_name = d.pop("workflow_name")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        workflow_event_replay_backfill_request = cls(
            workflow_name=workflow_name,
            from_=from_,
            until=until,
        )

        return workflow_event_replay_backfill_request
