from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_binding_change_response_change import (
    ProjectEnvironmentBindingChangeResponseChange,
    check_project_environment_binding_change_response_change,
)
from ..models.project_environment_binding_change_response_kind import (
    ProjectEnvironmentBindingChangeResponseKind,
    check_project_environment_binding_change_response_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_binding_response import ProjectEnvironmentBindingResponse


T = TypeVar("T", bound="ProjectEnvironmentBindingChangeResponse")


@_attrs_define
class ProjectEnvironmentBindingChangeResponse:
    """Managed resource binding change between two environment states."""

    kind: ProjectEnvironmentBindingChangeResponseKind
    binding_id: str
    change: ProjectEnvironmentBindingChangeResponseChange
    before: ProjectEnvironmentBindingResponse | Unset = UNSET
    """Managed resource binding and its target-scoped credential metadata."""
    after: ProjectEnvironmentBindingResponse | Unset = UNSET
    """Managed resource binding and its target-scoped credential metadata."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        binding_id = self.binding_id

        change: str = self.change

        before: dict[str, Any] | Unset = UNSET
        if not isinstance(self.before, Unset):
            before = self.before.to_dict()

        after: dict[str, Any] | Unset = UNSET
        if not isinstance(self.after, Unset):
            after = self.after.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "binding_id": binding_id,
                "change": change,
            }
        )
        if before is not UNSET:
            field_dict["before"] = before
        if after is not UNSET:
            field_dict["after"] = after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_binding_response import ProjectEnvironmentBindingResponse

        d = dict(src_dict)
        kind = check_project_environment_binding_change_response_kind(d.pop("kind"))

        binding_id = d.pop("binding_id")

        change = check_project_environment_binding_change_response_change(d.pop("change"))

        _before = d.pop("before", UNSET)
        before: ProjectEnvironmentBindingResponse | Unset
        if isinstance(_before, Unset):
            before = UNSET
        else:
            before = ProjectEnvironmentBindingResponse.from_dict(_before)

        _after = d.pop("after", UNSET)
        after: ProjectEnvironmentBindingResponse | Unset
        if isinstance(_after, Unset):
            after = UNSET
        else:
            after = ProjectEnvironmentBindingResponse.from_dict(_after)

        project_environment_binding_change_response = cls(
            kind=kind,
            binding_id=binding_id,
            change=change,
            before=before,
            after=after,
        )

        project_environment_binding_change_response.additional_properties = d
        return project_environment_binding_change_response

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
