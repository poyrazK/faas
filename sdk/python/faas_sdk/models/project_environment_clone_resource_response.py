from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_clone_resource_response_status import (
    ProjectEnvironmentCloneResourceResponseStatus,
    check_project_environment_clone_resource_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentCloneResourceResponse")


@_attrs_define
class ProjectEnvironmentCloneResourceResponse:
    """Progress and named blockers for an individual resource in a complete environment clone."""

    kind: str
    name: str
    status: ProjectEnvironmentCloneResourceResponseStatus
    source_id: str | Unset = UNSET
    source_version: str | Unset = UNSET
    target_id: str | Unset = UNSET
    capture_point: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind = self.kind

        name = self.name

        status: str = self.status

        source_id = self.source_id

        source_version = self.source_version

        target_id = self.target_id

        capture_point = self.capture_point

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "name": name,
                "status": status,
            }
        )
        if source_id is not UNSET:
            field_dict["source_id"] = source_id
        if source_version is not UNSET:
            field_dict["source_version"] = source_version
        if target_id is not UNSET:
            field_dict["target_id"] = target_id
        if capture_point is not UNSET:
            field_dict["capture_point"] = capture_point

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = d.pop("kind")

        name = d.pop("name")

        status = check_project_environment_clone_resource_response_status(d.pop("status"))

        source_id = d.pop("source_id", UNSET)

        source_version = d.pop("source_version", UNSET)

        target_id = d.pop("target_id", UNSET)

        capture_point = d.pop("capture_point", UNSET)

        project_environment_clone_resource_response = cls(
            kind=kind,
            name=name,
            status=status,
            source_id=source_id,
            source_version=source_version,
            target_id=target_id,
            capture_point=capture_point,
        )

        project_environment_clone_resource_response.additional_properties = d
        return project_environment_clone_resource_response

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
