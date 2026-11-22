from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_pipeline_event import ExecutionPipelineEvent
    from ..models.execution_pipeline_progress import ExecutionPipelineProgress


T = TypeVar("T", bound="ExecutionPipelineEventsResponse")


@_attrs_define
class ExecutionPipelineEventsResponse:
    """Resumable page of pipeline journal entries with the current state and gap information."""

    pipeline_id: UUID
    events: list[ExecutionPipelineEvent]
    next_sequence: int
    terminal: bool
    gap: bool | Unset = UNSET
    progress: ExecutionPipelineProgress | Unset = UNSET
    """Current pipeline sequence and observed progress for each step."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pipeline_id = str(self.pipeline_id)

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        next_sequence = self.next_sequence

        terminal = self.terminal

        gap = self.gap

        progress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.progress, Unset):
            progress = self.progress.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pipeline_id": pipeline_id,
                "events": events,
                "next_sequence": next_sequence,
                "terminal": terminal,
            }
        )
        if gap is not UNSET:
            field_dict["gap"] = gap
        if progress is not UNSET:
            field_dict["progress"] = progress

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_event import ExecutionPipelineEvent
        from ..models.execution_pipeline_progress import ExecutionPipelineProgress

        d = dict(src_dict)
        pipeline_id = UUID(d.pop("pipeline_id"))

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = ExecutionPipelineEvent.from_dict(events_item_data)

            events.append(events_item)

        next_sequence = d.pop("next_sequence")

        terminal = d.pop("terminal")

        gap = d.pop("gap", UNSET)

        _progress = d.pop("progress", UNSET)
        progress: ExecutionPipelineProgress | Unset
        if isinstance(_progress, Unset):
            progress = UNSET
        else:
            progress = ExecutionPipelineProgress.from_dict(_progress)

        execution_pipeline_events_response = cls(
            pipeline_id=pipeline_id,
            events=events,
            next_sequence=next_sequence,
            terminal=terminal,
            gap=gap,
            progress=progress,
        )

        execution_pipeline_events_response.additional_properties = d
        return execution_pipeline_events_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
