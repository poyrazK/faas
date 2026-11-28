from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_receipt_execution_response import EventReceiptExecutionResponse


T = TypeVar("T", bound="EventReceiptRecoveryResponse")


@_attrs_define
class EventReceiptRecoveryResponse:
    """Latest retained generic replay and history; original execution remains separate. Completed means this latest replay
    recovered; counts may decrease as execution records expire. Absence does not prove that no historical replay
    occurred.

    """

    retained_replay_count: int
    latest_replay: EventReceiptExecutionResponse
    """Current retained invocation state. The recipient execution is the original deterministic invocation;
    recovery and history contain trusted generic replay invocations."""
    history_url: str
    """Account-authenticated paginated replay history for this recipient."""

    def to_dict(self) -> dict[str, Any]:
        retained_replay_count = self.retained_replay_count

        latest_replay = self.latest_replay.to_dict()

        history_url = self.history_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "retained_replay_count": retained_replay_count,
                "latest_replay": latest_replay,
                "history_url": history_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_receipt_execution_response import EventReceiptExecutionResponse

        d = dict(src_dict)
        retained_replay_count = d.pop("retained_replay_count")

        latest_replay = EventReceiptExecutionResponse.from_dict(d.pop("latest_replay"))

        history_url = d.pop("history_url")

        event_receipt_recovery_response = cls(
            retained_replay_count=retained_replay_count,
            latest_replay=latest_replay,
            history_url=history_url,
        )

        return event_receipt_recovery_response
