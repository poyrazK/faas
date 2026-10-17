from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_git_recovery_release import EnvironmentGitRecoveryRelease


T = TypeVar("T", bound="ListEnvironmentGitReleasesResponse")


@_attrs_define
class ListEnvironmentGitReleasesResponse:
    """Read-only page of recovery eligibility observations for one verified target manifest commit."""

    project: str
    """Registered project slug for this recovery page."""
    environment: str
    """Registered environment slug for the retained recovery history."""
    source_id: UUID
    """Current Git binding identity restricting all releases and the page cursor."""
    target_commit_sha: str
    """Provider-verified immutable target manifest commit for every release assessment in the page."""
    observed_at: datetime.datetime
    """Time of this page's recovery observation; eligibility is not a future guarantee."""
    read_only: bool
    """Confirms that listing recovery history caused no retention or execution mutation."""
    values_redacted: bool
    """Confirms that recovery results omit configuration values, secret references and storage paths."""
    requires_target_qualification: bool
    """Recovery proposals must still pass new qualification for the target environment."""
    releases: list[EnvironmentGitRecoveryRelease]
    """Newest retained completed serving releases from this Git source, including expired windows."""
    next_before: UUID | Unset = UNSET
    """Last release UUID in this page when additional older recovery history exists."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project = self.project

        environment = self.environment

        source_id = str(self.source_id)

        target_commit_sha = self.target_commit_sha

        observed_at = self.observed_at.isoformat()

        read_only = self.read_only

        values_redacted = self.values_redacted

        requires_target_qualification = self.requires_target_qualification

        releases = []
        for releases_item_data in self.releases:
            releases_item = releases_item_data.to_dict()
            releases.append(releases_item)

        next_before: str | Unset = UNSET
        if not isinstance(self.next_before, Unset):
            next_before = str(self.next_before)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project": project,
                "environment": environment,
                "source_id": source_id,
                "target_commit_sha": target_commit_sha,
                "observed_at": observed_at,
                "read_only": read_only,
                "values_redacted": values_redacted,
                "requires_target_qualification": requires_target_qualification,
                "releases": releases,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_recovery_release import EnvironmentGitRecoveryRelease

        d = dict(src_dict)
        project = d.pop("project")

        environment = d.pop("environment")

        source_id = UUID(d.pop("source_id"))

        target_commit_sha = d.pop("target_commit_sha")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        read_only = d.pop("read_only")

        values_redacted = d.pop("values_redacted")

        requires_target_qualification = d.pop("requires_target_qualification")

        releases = []
        _releases = d.pop("releases")
        for releases_item_data in _releases:
            releases_item = EnvironmentGitRecoveryRelease.from_dict(releases_item_data)

            releases.append(releases_item)

        _next_before = d.pop("next_before", UNSET)
        next_before: UUID | Unset
        if isinstance(_next_before, Unset):
            next_before = UNSET
        else:
            next_before = UUID(_next_before)

        list_environment_git_releases_response = cls(
            project=project,
            environment=environment,
            source_id=source_id,
            target_commit_sha=target_commit_sha,
            observed_at=observed_at,
            read_only=read_only,
            values_redacted=values_redacted,
            requires_target_qualification=requires_target_qualification,
            releases=releases,
            next_before=next_before,
        )

        list_environment_git_releases_response.additional_properties = d
        return list_environment_git_releases_response

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
