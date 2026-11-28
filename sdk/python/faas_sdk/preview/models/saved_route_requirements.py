from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.route_requirements_config import RouteRequirementsConfig


T = TypeVar("T", bound="SavedRouteRequirements")


@_attrs_define
class SavedRouteRequirements:
    """Current version 2 route intent. Revision increments only when normalized intent changes. Original public rationale
    is never stored or returned.

    """

    app_id: UUID
    revision: int
    sha256: str
    requirements: RouteRequirementsConfig
    """Version 1 requires 1..500 concrete routes. Version 2 assigns every captured operation to groups, concrete
    routes, or public exceptions; overlapping groups are conjunctive."""
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        revision = self.revision

        sha256 = self.sha256

        requirements = self.requirements.to_dict()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "revision": revision,
                "sha256": sha256,
                "requirements": requirements,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_requirements_config import RouteRequirementsConfig

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        revision = d.pop("revision")

        sha256 = d.pop("sha256")

        requirements = RouteRequirementsConfig.from_dict(d.pop("requirements"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        saved_route_requirements = cls(
            app_id=app_id,
            revision=revision,
            sha256=sha256,
            requirements=requirements,
            updated_at=updated_at,
        )

        saved_route_requirements.additional_properties = d
        return saved_route_requirements

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
