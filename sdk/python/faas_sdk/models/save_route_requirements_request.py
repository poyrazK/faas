from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.route_requirements_config import RouteRequirementsConfig


T = TypeVar("T", bound="SaveRouteRequirementsRequest")


@_attrs_define
class SaveRouteRequirementsRequest:
    """Replacement version 2 route requirements with an optimistic revision check."""

    expected_revision: int
    """Current revision; 0 creates the first saved record."""
    requirements: RouteRequirementsConfig
    """Version 1 requires 1..500 concrete routes. Version 2 assigns every captured operation to groups, concrete
    routes, or public exceptions; overlapping groups are conjunctive."""

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        requirements = self.requirements.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "requirements": requirements,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_requirements_config import RouteRequirementsConfig

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        requirements = RouteRequirementsConfig.from_dict(d.pop("requirements"))

        save_route_requirements_request = cls(
            expected_revision=expected_revision,
            requirements=requirements,
        )

        return save_route_requirements_request
