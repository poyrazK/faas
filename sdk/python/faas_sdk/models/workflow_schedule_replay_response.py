from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_schedule_replay_outcome import WorkflowScheduleReplayOutcome


T = TypeVar("T", bound="WorkflowScheduleReplayResponse")


@_attrs_define
class WorkflowScheduleReplayResponse:
    """Per-occurrence results for a controlled schedule replay request."""

    outcomes: list[WorkflowScheduleReplayOutcome]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        outcomes = []
        for outcomes_item_data in self.outcomes:
            outcomes_item = outcomes_item_data.to_dict()
            outcomes.append(outcomes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "outcomes": outcomes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_schedule_replay_outcome import WorkflowScheduleReplayOutcome

        d = dict(src_dict)
        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = WorkflowScheduleReplayOutcome.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        workflow_schedule_replay_response = cls(
            outcomes=outcomes,
        )

        workflow_schedule_replay_response.additional_properties = d
        return workflow_schedule_replay_response

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
