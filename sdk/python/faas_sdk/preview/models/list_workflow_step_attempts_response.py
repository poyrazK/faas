from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_step_attempt_response import WorkflowStepAttemptResponse


T = TypeVar("T", bound="ListWorkflowStepAttemptsResponse")


@_attrs_define
class ListWorkflowStepAttemptsResponse:
    """The ordered executor-attempt history for one workflow step."""

    attempts: list[WorkflowStepAttemptResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempts = []
        for attempts_item_data in self.attempts:
            attempts_item = attempts_item_data.to_dict()
            attempts.append(attempts_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempts": attempts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_step_attempt_response import WorkflowStepAttemptResponse

        d = dict(src_dict)
        attempts = []
        _attempts = d.pop("attempts")
        for attempts_item_data in _attempts:
            attempts_item = WorkflowStepAttemptResponse.from_dict(attempts_item_data)

            attempts.append(attempts_item)

        list_workflow_step_attempts_response = cls(
            attempts=attempts,
        )

        list_workflow_step_attempts_response.additional_properties = d
        return list_workflow_step_attempts_response

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
