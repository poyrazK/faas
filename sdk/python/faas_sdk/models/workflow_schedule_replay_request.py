from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="WorkflowScheduleReplayRequest")


@_attrs_define
class WorkflowScheduleReplayRequest:
    """Selection of skipped schedule occurrence IDs to preview or replay."""

    occurrence_ids: list[UUID]

    def to_dict(self) -> dict[str, Any]:
        occurrence_ids = []
        for occurrence_ids_item_data in self.occurrence_ids:
            occurrence_ids_item = str(occurrence_ids_item_data)
            occurrence_ids.append(occurrence_ids_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "occurrence_ids": occurrence_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        occurrence_ids = []
        _occurrence_ids = d.pop("occurrence_ids")
        for occurrence_ids_item_data in _occurrence_ids:
            occurrence_ids_item = UUID(occurrence_ids_item_data)

            occurrence_ids.append(occurrence_ids_item)

        workflow_schedule_replay_request = cls(
            occurrence_ids=occurrence_ids,
        )

        return workflow_schedule_replay_request
