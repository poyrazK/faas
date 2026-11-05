from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ManagedExecutionWorkflowArtifactInput")


@_attrs_define
class ManagedExecutionWorkflowArtifactInput:
    """Select a declared output from an earlier workflow step and stage it at a relative path in this step's ephemeral
    bundle.

    """

    from_step: str
    name: str
    """Normalized output_files name declared by the producer step."""
    path: str
    """Normalized relative destination path in the consumer Run's ephemeral files bundle."""

    def to_dict(self) -> dict[str, Any]:
        from_step = self.from_step

        name = self.name

        path = self.path

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "from_step": from_step,
                "name": name,
                "path": path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_step = d.pop("from_step")

        name = d.pop("name")

        path = d.pop("path")

        managed_execution_workflow_artifact_input = cls(
            from_step=from_step,
            name=name,
            path=path,
        )

        return managed_execution_workflow_artifact_input
