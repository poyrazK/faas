from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_edge_policy_diff_response_kind import (
    ProjectEnvironmentEdgePolicyDiffResponseKind,
    check_project_environment_edge_policy_diff_response_kind,
)

if TYPE_CHECKING:
    from ..models.project_environment_edge_policy_response import ProjectEnvironmentEdgePolicyResponse


T = TypeVar("T", bound="ProjectEnvironmentEdgePolicyDiffResponse")


@_attrs_define
class ProjectEnvironmentEdgePolicyDiffResponse:
    """Difference in one environment edge-policy group or its ownership."""

    kind: ProjectEnvironmentEdgePolicyDiffResponseKind
    before: ProjectEnvironmentEdgePolicyResponse
    """Ownership and rules for one independently replaceable edge-policy group."""
    after: ProjectEnvironmentEdgePolicyResponse
    """Ownership and rules for one independently replaceable edge-policy group."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        before = self.before.to_dict()

        after = self.after.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "before": before,
                "after": after,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_edge_policy_response import ProjectEnvironmentEdgePolicyResponse

        d = dict(src_dict)
        kind = check_project_environment_edge_policy_diff_response_kind(d.pop("kind"))

        before = ProjectEnvironmentEdgePolicyResponse.from_dict(d.pop("before"))

        after = ProjectEnvironmentEdgePolicyResponse.from_dict(d.pop("after"))

        project_environment_edge_policy_diff_response = cls(
            kind=kind,
            before=before,
            after=after,
        )

        project_environment_edge_policy_diff_response.additional_properties = d
        return project_environment_edge_policy_diff_response

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
