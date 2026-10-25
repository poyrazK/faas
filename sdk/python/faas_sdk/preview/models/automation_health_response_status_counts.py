from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AutomationHealthResponseStatusCounts")


@_attrs_define
class AutomationHealthResponseStatusCounts:
    pending: int
    running: int
    awaiting_event: int
    succeeded: int
    failed: int
    dead: int

    def to_dict(self) -> dict[str, Any]:
        pending = self.pending

        running = self.running

        awaiting_event = self.awaiting_event

        succeeded = self.succeeded

        failed = self.failed

        dead = self.dead

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "pending": pending,
                "running": running,
                "awaiting_event": awaiting_event,
                "succeeded": succeeded,
                "failed": failed,
                "dead": dead,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pending = d.pop("pending")

        running = d.pop("running")

        awaiting_event = d.pop("awaiting_event")

        succeeded = d.pop("succeeded")

        failed = d.pop("failed")

        dead = d.pop("dead")

        automation_health_response_status_counts = cls(
            pending=pending,
            running=running,
            awaiting_event=awaiting_event,
            succeeded=succeeded,
            failed=failed,
            dead=dead,
        )

        return automation_health_response_status_counts
