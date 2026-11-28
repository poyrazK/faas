from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_resume_response import WorkflowResumeResponse


T = TypeVar("T", bound="ListWorkflowResumesResponse")


@_attrs_define
class ListWorkflowResumesResponse:
    """Bounded continuation history for an owned workflow run."""

    resumes: list[WorkflowResumeResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        resumes = []
        for resumes_item_data in self.resumes:
            resumes_item = resumes_item_data.to_dict()
            resumes.append(resumes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "resumes": resumes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_resume_response import WorkflowResumeResponse

        d = dict(src_dict)
        resumes = []
        _resumes = d.pop("resumes")
        for resumes_item_data in _resumes:
            resumes_item = WorkflowResumeResponse.from_dict(resumes_item_data)

            resumes.append(resumes_item)

        list_workflow_resumes_response = cls(
            resumes=resumes,
        )

        list_workflow_resumes_response.additional_properties = d
        return list_workflow_resumes_response

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
