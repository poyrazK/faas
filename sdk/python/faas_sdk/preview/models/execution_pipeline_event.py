from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_pipeline_event_type import ExecutionPipelineEventType, check_execution_pipeline_event_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_pipeline_step_progress import ExecutionPipelineStepProgress


T = TypeVar("T", bound="ExecutionPipelineEvent")


@_attrs_define
class ExecutionPipelineEvent:
    """One ordered, metadata-only update to a pipeline journal."""

    sequence: int
    type_: ExecutionPipelineEventType
    created_at: datetime.datetime
    status: str | Unset = UNSET
    step: ExecutionPipelineStepProgress | Unset = UNSET
    """Current state of a named step and its latest attempt metadata."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        type_: str = self.type_

        created_at = self.created_at.isoformat()

        status = self.status

        step: dict[str, Any] | Unset = UNSET
        if not isinstance(self.step, Unset):
            step = self.step.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "type": type_,
                "created_at": created_at,
            }
        )
        if status is not UNSET:
            field_dict["status"] = status
        if step is not UNSET:
            field_dict["step"] = step

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_step_progress import ExecutionPipelineStepProgress

        d = dict(src_dict)
        sequence = d.pop("sequence")

        type_ = check_execution_pipeline_event_type(d.pop("type"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        status = d.pop("status", UNSET)

        _step = d.pop("step", UNSET)
        step: ExecutionPipelineStepProgress | Unset
        if isinstance(_step, Unset):
            step = UNSET
        else:
            step = ExecutionPipelineStepProgress.from_dict(_step)

        execution_pipeline_event = cls(
            sequence=sequence,
            type_=type_,
            created_at=created_at,
            status=status,
            step=step,
        )

        execution_pipeline_event.additional_properties = d
        return execution_pipeline_event

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
