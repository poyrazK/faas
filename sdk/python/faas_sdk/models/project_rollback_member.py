from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectRollbackMember")


@_attrs_define
class ProjectRollbackMember:
    """A service revision recorded in a production project deploy."""

    deployment_id: UUID
    revision: int
    scope: str
    status: str
    previously_served: bool
    """ Whether the revision has a recorded end to a past serving period. """
    slug: str | Unset = UNSET
    """ App slug when the app is still attached to the project. """
    predecessor_id: UUID | Unset = UNSET
    predecessor_revision: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        revision = self.revision

        scope = self.scope

        status = self.status

        previously_served = self.previously_served

        slug = self.slug

        predecessor_id: str | Unset = UNSET
        if not isinstance(self.predecessor_id, Unset):
            predecessor_id = str(self.predecessor_id)

        predecessor_revision = self.predecessor_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "revision": revision,
                "scope": scope,
                "status": status,
                "previously_served": previously_served,
            }
        )
        if slug is not UNSET:
            field_dict["slug"] = slug
        if predecessor_id is not UNSET:
            field_dict["predecessor_id"] = predecessor_id
        if predecessor_revision is not UNSET:
            field_dict["predecessor_revision"] = predecessor_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        revision = d.pop("revision")

        scope = d.pop("scope")

        status = d.pop("status")

        previously_served = d.pop("previously_served")

        slug = d.pop("slug", UNSET)

        _predecessor_id = d.pop("predecessor_id", UNSET)
        predecessor_id: UUID | Unset
        if isinstance(_predecessor_id, Unset):
            predecessor_id = UNSET
        else:
            predecessor_id = UUID(_predecessor_id)

        predecessor_revision = d.pop("predecessor_revision", UNSET)

        project_rollback_member = cls(
            deployment_id=deployment_id,
            revision=revision,
            scope=scope,
            status=status,
            previously_served=previously_served,
            slug=slug,
            predecessor_id=predecessor_id,
            predecessor_revision=predecessor_revision,
        )

        project_rollback_member.additional_properties = d
        return project_rollback_member

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
