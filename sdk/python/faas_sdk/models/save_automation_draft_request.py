from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.workflow_spec import WorkflowSpec


T = TypeVar("T", bound="SaveAutomationDraftRequest")


@_attrs_define
class SaveAutomationDraftRequest:
    """Draft definition and the revision that the caller edited."""

    expected_version: int
    """Saved revision observed by the editor; zero creates the first draft."""
    definition: WorkflowSpec
    """A named workflow DAG submitted with a deployment (ADR-081). max_concurrent_runs caps active run instances
    for this workflow; excess admitted runs remain pending until a slot opens, subject to the app plan's run quota.
    max_concurrent_actions caps active executor steps across runs of this workflow; steps wait in the scheduler
    queue while all action slots are occupied."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        definition = self.definition.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "definition": definition,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_spec import WorkflowSpec

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        definition = WorkflowSpec.from_dict(d.pop("definition"))

        save_automation_draft_request = cls(
            expected_version=expected_version,
            definition=definition,
        )

        return save_automation_draft_request
