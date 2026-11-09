from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.stored_workflow_simulation_step import StoredWorkflowSimulationStep


T = TypeVar("T", bound="SimulateStoredWorkflowResponse")


@_attrs_define
class SimulateStoredWorkflowResponse:
    """Definition validity, submitted-definition SHA-256 and deterministic simulated data flow."""

    definition_valid: bool
    """Whether the definition and managed integration bindings passed validation."""
    definition_hash: str
    """SHA-256 of the JSON-serialized submitted definition, independent of samples; not a publication revision."""
    complete: bool
    """Every root reached a known terminal outcome under these mocks; false for missing results, unresolved
    retries, waits or evaluation errors."""
    issues: list[str]
    warnings: list[str]
    step_order: list[str]
    trace: list[StoredWorkflowSimulationStep]

    def to_dict(self) -> dict[str, Any]:
        definition_valid = self.definition_valid

        definition_hash = self.definition_hash

        complete = self.complete

        issues = self.issues

        warnings = self.warnings

        step_order = self.step_order

        trace = []
        for trace_item_data in self.trace:
            trace_item = trace_item_data.to_dict()
            trace.append(trace_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "definition_valid": definition_valid,
                "definition_hash": definition_hash,
                "complete": complete,
                "issues": issues,
                "warnings": warnings,
                "step_order": step_order,
                "trace": trace,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.stored_workflow_simulation_step import StoredWorkflowSimulationStep

        d = dict(src_dict)
        definition_valid = d.pop("definition_valid")

        definition_hash = d.pop("definition_hash")

        complete = d.pop("complete")

        issues = cast(list[str], d.pop("issues"))

        warnings = cast(list[str], d.pop("warnings"))

        step_order = cast(list[str], d.pop("step_order"))

        trace = []
        _trace = d.pop("trace")
        for trace_item_data in _trace:
            trace_item = StoredWorkflowSimulationStep.from_dict(trace_item_data)

            trace.append(trace_item)

        simulate_stored_workflow_response = cls(
            definition_valid=definition_valid,
            definition_hash=definition_hash,
            complete=complete,
            issues=issues,
            warnings=warnings,
            step_order=step_order,
            trace=trace,
        )

        return simulate_stored_workflow_response
