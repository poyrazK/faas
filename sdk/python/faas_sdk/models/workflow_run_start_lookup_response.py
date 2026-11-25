from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="WorkflowRunStartLookupResponse")


@_attrs_define
class WorkflowRunStartLookupResponse:
    """The retained run ID for a previously accepted pipeline-tool start."""

    run_id: UUID

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "run_id": run_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        workflow_run_start_lookup_response = cls(
            run_id=run_id,
        )

        return workflow_run_start_lookup_response
