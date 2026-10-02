from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_check_changes_status import RouteCheckChangesStatus, check_route_check_changes_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_check_change_summary import RouteCheckChangeSummary
    from ..models.route_finding_change import RouteFindingChange


T = TypeVar("T", bound="RouteCheckChanges")


@_attrs_define
class RouteCheckChanges:
    """Deterministic bounded comparison with exact summary counts. Initial checks and intent changes establish a baseline
    without claiming resolution. Truncated detail never changes summary counts. Previous observations can outlive
    retained history.

    """

    version: int
    check_id: UUID
    status: RouteCheckChangesStatus
    summary: RouteCheckChangeSummary
    truncated: bool
    findings: list[RouteFindingChange]
    compared_to_check_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        check_id = str(self.check_id)

        status: str = self.status

        summary = self.summary.to_dict()

        truncated = self.truncated

        findings = []
        for findings_item_data in self.findings:
            findings_item = findings_item_data.to_dict()
            findings.append(findings_item)

        compared_to_check_id: str | Unset = UNSET
        if not isinstance(self.compared_to_check_id, Unset):
            compared_to_check_id = str(self.compared_to_check_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "check_id": check_id,
                "status": status,
                "summary": summary,
                "truncated": truncated,
                "findings": findings,
            }
        )
        if compared_to_check_id is not UNSET:
            field_dict["compared_to_check_id"] = compared_to_check_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_check_change_summary import RouteCheckChangeSummary
        from ..models.route_finding_change import RouteFindingChange

        d = dict(src_dict)
        version = d.pop("version")

        check_id = UUID(d.pop("check_id"))

        status = check_route_check_changes_status(d.pop("status"))

        summary = RouteCheckChangeSummary.from_dict(d.pop("summary"))

        truncated = d.pop("truncated")

        findings = []
        _findings = d.pop("findings")
        for findings_item_data in _findings:
            findings_item = RouteFindingChange.from_dict(findings_item_data)

            findings.append(findings_item)

        _compared_to_check_id = d.pop("compared_to_check_id", UNSET)
        compared_to_check_id: UUID | Unset
        if isinstance(_compared_to_check_id, Unset):
            compared_to_check_id = UNSET
        else:
            compared_to_check_id = UUID(_compared_to_check_id)

        route_check_changes = cls(
            version=version,
            check_id=check_id,
            status=status,
            summary=summary,
            truncated=truncated,
            findings=findings,
            compared_to_check_id=compared_to_check_id,
        )

        route_check_changes.additional_properties = d
        return route_check_changes

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
