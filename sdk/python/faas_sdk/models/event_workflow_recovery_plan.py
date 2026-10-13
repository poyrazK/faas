from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_workflow_recovery_target import EventWorkflowRecoveryTarget


T = TypeVar("T", bound="EventWorkflowRecoveryPlan")


@_attrs_define
class EventWorkflowRecoveryPlan:
    """Explicit reviewed workflow acceptance recipients. Routing mode only, requires request_id and excludes parent and
    backlog filters. Creation freezes the exact selection. Before each admission the worker atomically compares
    published version, replay count and routing generation; changes are skipped. Repeated request_id returns the
    retained job, even after completion; a different normalized plan or rate conflicts. Preview and preflight are
    advisory; workflow fence eligibility is unknown in preflight. Queued means routing recovery was admitted, not
    workflow completion.

    """

    name: str
    published_version: int
    source: str
    events: list[EventWorkflowRecoveryTarget]

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        published_version = self.published_version

        source = self.source

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "published_version": published_version,
                "source": source,
                "events": events,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_workflow_recovery_target import EventWorkflowRecoveryTarget

        d = dict(src_dict)
        name = d.pop("name")

        published_version = d.pop("published_version")

        source = d.pop("source")

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = EventWorkflowRecoveryTarget.from_dict(events_item_data)

            events.append(events_item)

        event_workflow_recovery_plan = cls(
            name=name,
            published_version=published_version,
            source=source,
            events=events,
        )

        return event_workflow_recovery_plan
