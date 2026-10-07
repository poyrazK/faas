from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_queued_run_cancel_outcome import WorkflowQueuedRunCancelOutcome


T = TypeVar("T", bound="WorkflowQueuedRunCancelResponse")


@_attrs_define
class WorkflowQueuedRunCancelResponse:
    outcomes: list[WorkflowQueuedRunCancelOutcome]
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
        from ..models.workflow_queued_run_cancel_outcome import WorkflowQueuedRunCancelOutcome

        d = dict(src_dict)
        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = WorkflowQueuedRunCancelOutcome.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        workflow_queued_run_cancel_response = cls(
            outcomes=outcomes,
        )

        workflow_queued_run_cancel_response.additional_properties = d
        return workflow_queued_run_cancel_response

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
