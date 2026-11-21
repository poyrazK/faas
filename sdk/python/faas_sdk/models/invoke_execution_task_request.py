from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_artifact_input import ExecutionArtifactInput
    from ..models.execution_file_input import ExecutionFileInput


T = TypeVar("T", bound="InvokeExecutionTaskRequest")


@_attrs_define
class InvokeExecutionTaskRequest:
    """Task selection, invocation input and optional file or completion configuration."""

    name: str
    version: int
    customer_ref: str | Unset = UNSET
    input_: Any | Unset = UNSET
    input_files: list[ExecutionFileInput] | Unset = UNSET
    artifact_inputs: list[ExecutionArtifactInput] | Unset = UNSET
    workflow_id: str | Unset = UNSET
    step_label: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        version = self.version

        customer_ref = self.customer_ref

        input_ = self.input_

        input_files: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.input_files, Unset):
            input_files = []
            for input_files_item_data in self.input_files:
                input_files_item = input_files_item_data.to_dict()
                input_files.append(input_files_item)

        artifact_inputs: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.artifact_inputs, Unset):
            artifact_inputs = []
            for artifact_inputs_item_data in self.artifact_inputs:
                artifact_inputs_item = artifact_inputs_item_data.to_dict()
                artifact_inputs.append(artifact_inputs_item)

        workflow_id = self.workflow_id

        step_label = self.step_label

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "version": version,
            }
        )
        if customer_ref is not UNSET:
            field_dict["customer_ref"] = customer_ref
        if input_ is not UNSET:
            field_dict["input"] = input_
        if input_files is not UNSET:
            field_dict["input_files"] = input_files
        if artifact_inputs is not UNSET:
            field_dict["artifact_inputs"] = artifact_inputs
        if workflow_id is not UNSET:
            field_dict["workflow_id"] = workflow_id
        if step_label is not UNSET:
            field_dict["step_label"] = step_label

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_artifact_input import ExecutionArtifactInput
        from ..models.execution_file_input import ExecutionFileInput

        d = dict(src_dict)
        name = d.pop("name")

        version = d.pop("version")

        customer_ref = d.pop("customer_ref", UNSET)

        input_ = d.pop("input", UNSET)

        _input_files = d.pop("input_files", UNSET)
        input_files: list[ExecutionFileInput] | Unset = UNSET
        if _input_files is not UNSET:
            input_files = []
            for input_files_item_data in _input_files:
                input_files_item = ExecutionFileInput.from_dict(input_files_item_data)

                input_files.append(input_files_item)

        _artifact_inputs = d.pop("artifact_inputs", UNSET)
        artifact_inputs: list[ExecutionArtifactInput] | Unset = UNSET
        if _artifact_inputs is not UNSET:
            artifact_inputs = []
            for artifact_inputs_item_data in _artifact_inputs:
                artifact_inputs_item = ExecutionArtifactInput.from_dict(artifact_inputs_item_data)

                artifact_inputs.append(artifact_inputs_item)

        workflow_id = d.pop("workflow_id", UNSET)

        step_label = d.pop("step_label", UNSET)

        invoke_execution_task_request = cls(
            name=name,
            version=version,
            customer_ref=customer_ref,
            input_=input_,
            input_files=input_files,
            artifact_inputs=artifact_inputs,
            workflow_id=workflow_id,
            step_label=step_label,
        )

        invoke_execution_task_request.additional_properties = d
        return invoke_execution_task_request

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
