from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.preview_environment_status_response_phase import (
    PreviewEnvironmentStatusResponsePhase,
    check_preview_environment_status_response_phase,
)

if TYPE_CHECKING:
    from ..models.preview_environment_member_response import PreviewEnvironmentMemberResponse


T = TypeVar("T", bound="PreviewEnvironmentStatusResponse")


@_attrs_define
class PreviewEnvironmentStatusResponse:
    """Aggregate current-head readiness for a recorded GitHub PR preview workload set."""

    root_slug: str
    repo_full_name: str
    pr_number: int
    commit_sha: str
    phase: PreviewEnvironmentStatusResponsePhase
    ready: bool
    summary: str
    live_workloads: int
    total_workloads: int
    members: list[PreviewEnvironmentMemberResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        root_slug = self.root_slug

        repo_full_name = self.repo_full_name

        pr_number = self.pr_number

        commit_sha = self.commit_sha

        phase: str = self.phase

        ready = self.ready

        summary = self.summary

        live_workloads = self.live_workloads

        total_workloads = self.total_workloads

        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "root_slug": root_slug,
                "repo_full_name": repo_full_name,
                "pr_number": pr_number,
                "commit_sha": commit_sha,
                "phase": phase,
                "ready": ready,
                "summary": summary,
                "live_workloads": live_workloads,
                "total_workloads": total_workloads,
                "members": members,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.preview_environment_member_response import PreviewEnvironmentMemberResponse

        d = dict(src_dict)
        root_slug = d.pop("root_slug")

        repo_full_name = d.pop("repo_full_name")

        pr_number = d.pop("pr_number")

        commit_sha = d.pop("commit_sha")

        phase = check_preview_environment_status_response_phase(d.pop("phase"))

        ready = d.pop("ready")

        summary = d.pop("summary")

        live_workloads = d.pop("live_workloads")

        total_workloads = d.pop("total_workloads")

        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = PreviewEnvironmentMemberResponse.from_dict(members_item_data)

            members.append(members_item)

        preview_environment_status_response = cls(
            root_slug=root_slug,
            repo_full_name=repo_full_name,
            pr_number=pr_number,
            commit_sha=commit_sha,
            phase=phase,
            ready=ready,
            summary=summary,
            live_workloads=live_workloads,
            total_workloads=total_workloads,
            members=members,
        )

        preview_environment_status_response.additional_properties = d
        return preview_environment_status_response

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
