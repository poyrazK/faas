from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_initial_secrets import ProjectInitialSecrets


T = TypeVar("T", bound="ResumeProjectMigrationRequest")


@_attrs_define
class ResumeProjectMigrationRequest:
    """Explicitly resume unfinished migration work against the original reviewed source checksum."""

    expected_version: int
    """Version of the saved receipt reviewed before this resume; stale replays are rejected."""
    only: list[str]
    """Exact unfinished services approved for this resume action."""
    plan_token: str
    """Fresh migration-mode token for the original source checksum and project."""
    initial_secrets: ProjectInitialSecrets | Unset = UNSET
    """Request-only mapping of exact selected workload names to initial sealed environment values. Values stay in
    memory until sealing and never enter responses or source caches."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        only = self.only

        plan_token = self.plan_token

        initial_secrets: dict[str, Any] | Unset = UNSET
        if not isinstance(self.initial_secrets, Unset):
            initial_secrets = self.initial_secrets.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "only": only,
                "plan_token": plan_token,
            }
        )
        if initial_secrets is not UNSET:
            field_dict["initial_secrets"] = initial_secrets

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_initial_secrets import ProjectInitialSecrets

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        only = cast(list[str], d.pop("only"))

        plan_token = d.pop("plan_token")

        _initial_secrets = d.pop("initial_secrets", UNSET)
        initial_secrets: ProjectInitialSecrets | Unset
        if isinstance(_initial_secrets, Unset):
            initial_secrets = UNSET
        else:
            initial_secrets = ProjectInitialSecrets.from_dict(_initial_secrets)

        resume_project_migration_request = cls(
            expected_version=expected_version,
            only=only,
            plan_token=plan_token,
            initial_secrets=initial_secrets,
        )

        return resume_project_migration_request
