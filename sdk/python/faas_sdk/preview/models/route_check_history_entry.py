from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.route_check_changes import RouteCheckChanges
    from ..models.route_requirements_check import RouteRequirementsCheck


T = TypeVar("T", bound="RouteCheckHistoryEntry")


@_attrs_define
class RouteCheckHistoryEntry:
    """Immutable retained completion evidence. Historical verdicts do not establish current safety. May expire under per-
    deployment retention caps.

    """

    version: int
    id: UUID
    checked_at: datetime.datetime
    check: RouteRequirementsCheck
    """Read-only coverage against one captured deployment and current configuration with saved intent provenance.
    Does not persist history or prove runtime behavior."""
    changes: RouteCheckChanges
    """Deterministic bounded comparison with exact summary counts. Initial checks and intent changes establish a
    baseline without claiming resolution. Truncated detail never changes summary counts. Previous observations can
    outlive retained history."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        id = str(self.id)

        checked_at = self.checked_at.isoformat()

        check = self.check.to_dict()

        changes = self.changes.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "id": id,
                "checked_at": checked_at,
                "check": check,
                "changes": changes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_check_changes import RouteCheckChanges
        from ..models.route_requirements_check import RouteRequirementsCheck

        d = dict(src_dict)
        version = d.pop("version")

        id = UUID(d.pop("id"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        check = RouteRequirementsCheck.from_dict(d.pop("check"))

        changes = RouteCheckChanges.from_dict(d.pop("changes"))

        route_check_history_entry = cls(
            version=version,
            id=id,
            checked_at=checked_at,
            check=check,
            changes=changes,
        )

        route_check_history_entry.additional_properties = d
        return route_check_history_entry

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
