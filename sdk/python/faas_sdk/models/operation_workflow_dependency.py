from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowDependency")


@_attrs_define
class OperationWorkflowDependency:
    """Direct prerequisite in the same application/customer/environment. References may have no retained state. Self
    references and duplicate targets are rejected.

    """

    subject_type: str
    subject_id: str
    workflow: str
    instance_id: str
    required_outcome_code: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subject_type = self.subject_type

        subject_id = self.subject_id

        workflow = self.workflow

        instance_id = self.instance_id

        required_outcome_code = self.required_outcome_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject_type": subject_type,
                "subject_id": subject_id,
                "workflow": workflow,
                "instance_id": instance_id,
            }
        )
        if required_outcome_code is not UNSET:
            field_dict["required_outcome_code"] = required_outcome_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subject_type = d.pop("subject_type")

        subject_id = d.pop("subject_id")

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        required_outcome_code = d.pop("required_outcome_code", UNSET)

        operation_workflow_dependency = cls(
            subject_type=subject_type,
            subject_id=subject_id,
            workflow=workflow,
            instance_id=instance_id,
            required_outcome_code=required_outcome_code,
        )

        return operation_workflow_dependency
