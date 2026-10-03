from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_finding_change_kind import RouteFindingChangeKind, check_route_finding_change_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_requirements_finding import RouteRequirementsFinding


T = TypeVar("T", bound="RouteFindingChange")


@_attrs_define
class RouteFindingChange:
    """One finding compared with its last known observation for unchanged saved intent. Removed findings are not fixes;
    unknown evidence retains the previous known verdict.

    """

    method: str
    path: str
    requirement: str
    kind: RouteFindingChangeKind
    before_check_id: UUID | Unset = UNSET
    before: RouteRequirementsFinding | Unset = UNSET
    """Allowlisted configuration summary without raw credential material or unrelated rule actions."""
    after: RouteRequirementsFinding | Unset = UNSET
    """Allowlisted configuration summary without raw credential material or unrelated rule actions."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        path = self.path

        requirement = self.requirement

        kind: str = self.kind

        before_check_id: str | Unset = UNSET
        if not isinstance(self.before_check_id, Unset):
            before_check_id = str(self.before_check_id)

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
                "method": method,
                "path": path,
                "requirement": requirement,
                "kind": kind,
            }
        )
        if before_check_id is not UNSET:
            field_dict["before_check_id"] = before_check_id
        if before is not UNSET:
            field_dict["before"] = before
        if after is not UNSET:
            field_dict["after"] = after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_requirements_finding import RouteRequirementsFinding

        d = dict(src_dict)
        method = d.pop("method")

        path = d.pop("path")

        requirement = d.pop("requirement")

        kind = check_route_finding_change_kind(d.pop("kind"))

        _before_check_id = d.pop("before_check_id", UNSET)
        before_check_id: UUID | Unset
        if isinstance(_before_check_id, Unset):
            before_check_id = UNSET
        else:
            before_check_id = UUID(_before_check_id)

        _before = d.pop("before", UNSET)
        before: RouteRequirementsFinding | Unset
        if isinstance(_before, Unset):
            before = UNSET
        else:
            before = RouteRequirementsFinding.from_dict(_before)

        _after = d.pop("after", UNSET)
        after: RouteRequirementsFinding | Unset
        if isinstance(_after, Unset):
            after = UNSET
        else:
            after = RouteRequirementsFinding.from_dict(_after)

        route_finding_change = cls(
            method=method,
            path=path,
            requirement=requirement,
            kind=kind,
            before_check_id=before_check_id,
            before=before,
            after=after,
        )

        route_finding_change.additional_properties = d
        return route_finding_change

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
