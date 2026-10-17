from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ProjectReleaseStageMember")


@_attrs_define
class ProjectReleaseStageMember:
    """Candidate service deployment and observed replica state covered by a staging lease."""

    app: str
    deployment_id: UUID
    desired: int
    ready: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        deployment_id = str(self.deployment_id)

        desired = self.desired

        ready = self.ready

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "deployment_id": deployment_id,
                "desired": desired,
                "ready": ready,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app = d.pop("app")

        deployment_id = UUID(d.pop("deployment_id"))

        desired = d.pop("desired")

        ready = d.pop("ready")

        project_release_stage_member = cls(
            app=app,
            deployment_id=deployment_id,
            desired=desired,
            ready=ready,
        )

        project_release_stage_member.additional_properties = d
        return project_release_stage_member

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
