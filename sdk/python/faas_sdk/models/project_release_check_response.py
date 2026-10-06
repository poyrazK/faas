from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_check_finding import BindingCheckFinding
    from ..models.binding_check_report import BindingCheckReport
    from ..models.project_release_set_member_response import ProjectReleaseSetMemberResponse


T = TypeVar("T", bound="ProjectReleaseCheckResponse")


@_attrs_define
class ProjectReleaseCheckResponse:
    """Observation of the complete exact deployment graph and its binding evidence. Publication evaluates fresh evidence
    and compares the active predecessor again.

    """

    project_id: UUID
    environment: str
    expected_active_release_id: str
    graph_digest: str
    ttl_seconds: int
    members: list[ProjectReleaseSetMemberResponse]
    passed: bool
    checked_at: datetime.datetime
    checks: list[BindingCheckReport]
    blockers: list[BindingCheckFinding] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_id = str(self.project_id)

        environment = self.environment

        expected_active_release_id = self.expected_active_release_id

        graph_digest = self.graph_digest

        ttl_seconds = self.ttl_seconds

        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        passed = self.passed

        checked_at = self.checked_at.isoformat()

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_id": project_id,
                "environment": environment,
                "expected_active_release_id": expected_active_release_id,
                "graph_digest": graph_digest,
                "ttl_seconds": ttl_seconds,
                "members": members,
                "passed": passed,
                "checked_at": checked_at,
                "checks": checks,
            }
        )
        if blockers is not UNSET:
            field_dict["blockers"] = blockers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_check_finding import BindingCheckFinding
        from ..models.binding_check_report import BindingCheckReport
        from ..models.project_release_set_member_response import ProjectReleaseSetMemberResponse

        d = dict(src_dict)
        project_id = UUID(d.pop("project_id"))

        environment = d.pop("environment")

        expected_active_release_id = d.pop("expected_active_release_id")

        graph_digest = d.pop("graph_digest")

        ttl_seconds = d.pop("ttl_seconds")

        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = ProjectReleaseSetMemberResponse.from_dict(members_item_data)

            members.append(members_item)

        passed = d.pop("passed")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = BindingCheckReport.from_dict(checks_item_data)

            checks.append(checks_item)

        _blockers = d.pop("blockers", UNSET)
        blockers: list[BindingCheckFinding] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = BindingCheckFinding.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        project_release_check_response = cls(
            project_id=project_id,
            environment=environment,
            expected_active_release_id=expected_active_release_id,
            graph_digest=graph_digest,
            ttl_seconds=ttl_seconds,
            members=members,
            passed=passed,
            checked_at=checked_at,
            checks=checks,
            blockers=blockers,
        )

        project_release_check_response.additional_properties = d
        return project_release_check_response

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
