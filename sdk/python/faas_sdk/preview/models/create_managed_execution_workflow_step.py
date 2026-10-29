from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_managed_execution_workflow_step_result_schema_type_0 import (
        CreateManagedExecutionWorkflowStepResultSchemaType0,
    )
    from ..models.managed_execution_workflow_artifact_input import ManagedExecutionWorkflowArtifactInput


T = TypeVar("T", bound="CreateManagedExecutionWorkflowStep")


@_attrs_define
class CreateManagedExecutionWorkflowStep:
    """One disposable Run in the DAG and its dependency, result, and artifact handoff rules."""

    label: str
    request: Any
    """Source and JSON input for one disposable execution. Send either the
    legacy `source` string or an ephemeral `files` bundle with an
    `entrypoint`. `artifact_inputs` may add files from successful runs in
    the same key family for Runs-only credentials, or from the same account
    for broad credentials; they are copied into the new
    encrypted request and staged only in its ephemeral guest filesystem.
    Optional `workflow_id` and `step_label` values group run receipts in
    control-plane metadata; the guest does not receive them.
    Optional `integration_ids` explicitly select managed integrations
    granted for this Run; the IDs remain control-plane metadata and are
    not delivered to the guest payload.
    v1 supports only the listed interpreter runtimes and
    `network.mode=none`; dependencies, secrets, environment injection, and
    persistent disks are not part of this contract.
    """
    depends_on: list[str] | Unset = UNSET
    """Earlier step labels that must succeed before this step is admitted."""
    input_from_previous_result: bool | Unset = False
    """Use the immediately preceding successful Run's JSON result as this step's input; adds that step as a
    dependency."""
    include_dependency_results: bool | Unset = False
    """Set input to a JSON object keyed by dependency label, with each dependency's terminal status and successful
    JSON result when available."""
    artifact_inputs: list[ManagedExecutionWorkflowArtifactInput] | Unset = UNSET
    """Artifacts from earlier successful steps to stage in this Run's fresh ephemeral files bundle; each producer
    becomes an implicit dependency."""
    result_schema: bool | CreateManagedExecutionWorkflowStepResultSchemaType0 | Unset = UNSET
    """Optional JSON Schema Draft 2020-12 contract for this Run's JSON result. The control plane enforces it before
    dependents can consume the result. Local fragment references are supported; external resources are not loaded.
    Each schema is limited to 64 KiB and all schemas in a workflow together are limited to 1 MiB."""

    def to_dict(self) -> dict[str, Any]:
        from ..models.create_managed_execution_workflow_step_result_schema_type_0 import (
            CreateManagedExecutionWorkflowStepResultSchemaType0,
        )

        label = self.label

        request: Any
        request = self.request

        depends_on: list[str] | Unset = UNSET
        if not isinstance(self.depends_on, Unset):
            depends_on = self.depends_on

        input_from_previous_result = self.input_from_previous_result

        include_dependency_results = self.include_dependency_results

        artifact_inputs: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.artifact_inputs, Unset):
            artifact_inputs = []
            for artifact_inputs_item_data in self.artifact_inputs:
                artifact_inputs_item = artifact_inputs_item_data.to_dict()
                artifact_inputs.append(artifact_inputs_item)

        result_schema: bool | dict[str, Any] | Unset
        if isinstance(self.result_schema, Unset):
            result_schema = UNSET
        elif isinstance(self.result_schema, CreateManagedExecutionWorkflowStepResultSchemaType0):
            result_schema = self.result_schema.to_dict()
        else:
            result_schema = self.result_schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "label": label,
                "request": request,
            }
        )
        if depends_on is not UNSET:
            field_dict["depends_on"] = depends_on
        if input_from_previous_result is not UNSET:
            field_dict["input_from_previous_result"] = input_from_previous_result
        if include_dependency_results is not UNSET:
            field_dict["include_dependency_results"] = include_dependency_results
        if artifact_inputs is not UNSET:
            field_dict["artifact_inputs"] = artifact_inputs
        if result_schema is not UNSET:
            field_dict["result_schema"] = result_schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_managed_execution_workflow_step_result_schema_type_0 import (
            CreateManagedExecutionWorkflowStepResultSchemaType0,
        )
        from ..models.managed_execution_workflow_artifact_input import ManagedExecutionWorkflowArtifactInput

        d = dict(src_dict)
        label = d.pop("label")

        def _parse_request(data: object) -> Any:
            return cast(Any, data)

        request = _parse_request(d.pop("request"))

        depends_on = cast(list[str], d.pop("depends_on", UNSET))

        input_from_previous_result = d.pop("input_from_previous_result", UNSET)

        include_dependency_results = d.pop("include_dependency_results", UNSET)

        _artifact_inputs = d.pop("artifact_inputs", UNSET)
        artifact_inputs: list[ManagedExecutionWorkflowArtifactInput] | Unset = UNSET
        if _artifact_inputs is not UNSET:
            artifact_inputs = []
            for artifact_inputs_item_data in _artifact_inputs:
                artifact_inputs_item = ManagedExecutionWorkflowArtifactInput.from_dict(artifact_inputs_item_data)

                artifact_inputs.append(artifact_inputs_item)

        def _parse_result_schema(data: object) -> bool | CreateManagedExecutionWorkflowStepResultSchemaType0 | Unset:
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                result_schema_type_0 = CreateManagedExecutionWorkflowStepResultSchemaType0.from_dict(data)

                return result_schema_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(bool | CreateManagedExecutionWorkflowStepResultSchemaType0 | Unset, data)

        result_schema = _parse_result_schema(d.pop("result_schema", UNSET))

        create_managed_execution_workflow_step = cls(
            label=label,
            request=request,
            depends_on=depends_on,
            input_from_previous_result=input_from_previous_result,
            include_dependency_results=include_dependency_results,
            artifact_inputs=artifact_inputs,
            result_schema=result_schema,
        )

        return create_managed_execution_workflow_step
