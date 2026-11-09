from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.stored_workflow_response import StoredWorkflowResponse


T = TypeVar("T", bound="ListStoredWorkflowsResponse")


@_attrs_define
class ListStoredWorkflowsResponse:
    """App stored workflow definitions, plan limit and execution availability."""

    app_slug: str
    runtime_enabled: bool
    max_definitions: int
    stored_workflows: list[StoredWorkflowResponse]
    unavailable_reason: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        runtime_enabled = self.runtime_enabled

        max_definitions = self.max_definitions

        stored_workflows = []
        for stored_workflows_item_data in self.stored_workflows:
            stored_workflows_item = stored_workflows_item_data.to_dict()
            stored_workflows.append(stored_workflows_item)

        unavailable_reason = self.unavailable_reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "runtime_enabled": runtime_enabled,
                "max_definitions": max_definitions,
                "stored_workflows": stored_workflows,
            }
        )
        if unavailable_reason is not UNSET:
            field_dict["unavailable_reason"] = unavailable_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.stored_workflow_response import StoredWorkflowResponse

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        runtime_enabled = d.pop("runtime_enabled")

        max_definitions = d.pop("max_definitions")

        stored_workflows = []
        _stored_workflows = d.pop("stored_workflows")
        for stored_workflows_item_data in _stored_workflows:
            stored_workflows_item = StoredWorkflowResponse.from_dict(stored_workflows_item_data)

            stored_workflows.append(stored_workflows_item)

        unavailable_reason = d.pop("unavailable_reason", UNSET)

        list_stored_workflows_response = cls(
            app_slug=app_slug,
            runtime_enabled=runtime_enabled,
            max_definitions=max_definitions,
            stored_workflows=stored_workflows,
            unavailable_reason=unavailable_reason,
        )

        return list_stored_workflows_response
