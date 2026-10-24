from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.workflow_spec import WorkflowSpec


T = TypeVar("T", bound="ValidateAutomationRequest")


@_attrs_define
class ValidateAutomationRequest:
    """Candidate automation definition for a read-only validation."""

    definition: WorkflowSpec
    """A named workflow DAG submitted with a deployment (ADR-081). max_concurrent_runs caps active run instances
    for this workflow; excess admitted runs remain pending until a slot opens, subject to the app plan's run quota.
    max_concurrent_actions caps active executor steps across runs of this workflow; steps wait in the scheduler
    queue while all action slots are occupied."""

    def to_dict(self) -> dict[str, Any]:
        definition = self.definition.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "definition": definition,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_spec import WorkflowSpec

        d = dict(src_dict)
        definition = WorkflowSpec.from_dict(d.pop("definition"))

        validate_automation_request = cls(
            definition=definition,
        )

        return validate_automation_request
