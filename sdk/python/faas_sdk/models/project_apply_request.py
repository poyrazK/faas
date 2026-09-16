from __future__ import annotations

from collections.abc import Mapping
from io import BytesIO
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from .. import types
from ..types import UNSET, File, Unset

T = TypeVar("T", bound="ProjectApplyRequest")


@_attrs_define
class ProjectApplyRequest:
    """Multipart body for POST /v1/projects (apply). Same shape as
    ProjectScanRequest; the apply handler resolves AppIDs and
    inserts crons in a follow-up pass.

    """

    source: File
    project_slug: str | Unset = UNSET
    repo_full_name: str | Unset = UNSET
    production_branch: str | Unset = UNSET
    install_id: int | Unset = UNSET
    only: str | Unset = UNSET
    environment: str | Unset = UNSET
    """Environment slug applied to deployments created by this apply"""
    approval_token: str | Unset = UNSET
    """Short-lived approval credential for the exact protected-environment plan"""
    no_triggers: bool | Unset = False
    """Leave trigger declarations and existing project trigger state unchanged for this apply."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source = self.source.to_tuple()

        project_slug = self.project_slug

        repo_full_name = self.repo_full_name

        production_branch = self.production_branch

        install_id = self.install_id

        only = self.only

        environment = self.environment

        approval_token = self.approval_token

        no_triggers = self.no_triggers

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
            }
        )
        if project_slug is not UNSET:
            field_dict["project_slug"] = project_slug
        if repo_full_name is not UNSET:
            field_dict["repo_full_name"] = repo_full_name
        if production_branch is not UNSET:
            field_dict["production_branch"] = production_branch
        if install_id is not UNSET:
            field_dict["install_id"] = install_id
        if only is not UNSET:
            field_dict["only"] = only
        if environment is not UNSET:
            field_dict["environment"] = environment
        if approval_token is not UNSET:
            field_dict["approval_token"] = approval_token
        if no_triggers is not UNSET:
            field_dict["no_triggers"] = no_triggers

        return field_dict

    def to_multipart(self) -> types.RequestFiles:
        files: types.RequestFiles = []

        files.append(("source", self.source.to_tuple()))

        if not isinstance(self.project_slug, Unset):
            files.append(("project_slug", (None, str(self.project_slug).encode(), "text/plain")))

        if not isinstance(self.repo_full_name, Unset):
            files.append(("repo_full_name", (None, str(self.repo_full_name).encode(), "text/plain")))

        if not isinstance(self.production_branch, Unset):
            files.append(("production_branch", (None, str(self.production_branch).encode(), "text/plain")))

        if not isinstance(self.install_id, Unset):
            files.append(("install_id", (None, str(self.install_id).encode(), "text/plain")))

        if not isinstance(self.only, Unset):
            files.append(("only", (None, str(self.only).encode(), "text/plain")))

        if not isinstance(self.environment, Unset):
            files.append(("environment", (None, str(self.environment).encode(), "text/plain")))

        if not isinstance(self.approval_token, Unset):
            files.append(("approval_token", (None, str(self.approval_token).encode(), "text/plain")))

        if not isinstance(self.no_triggers, Unset):
            files.append(("no_triggers", (None, str(self.no_triggers).encode(), "text/plain")))

        for prop_name, prop in self.additional_properties.items():
            files.append((prop_name, (None, str(prop).encode(), "text/plain")))

        return files

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = File(payload=BytesIO(d.pop("source")))

        project_slug = d.pop("project_slug", UNSET)

        repo_full_name = d.pop("repo_full_name", UNSET)

        production_branch = d.pop("production_branch", UNSET)

        install_id = d.pop("install_id", UNSET)

        only = d.pop("only", UNSET)

        environment = d.pop("environment", UNSET)

        approval_token = d.pop("approval_token", UNSET)

        no_triggers = d.pop("no_triggers", UNSET)

        project_apply_request = cls(
            source=source,
            project_slug=project_slug,
            repo_full_name=repo_full_name,
            production_branch=production_branch,
            install_id=install_id,
            only=only,
            environment=environment,
            approval_token=approval_token,
            no_triggers=no_triggers,
        )

        project_apply_request.additional_properties = d
        return project_apply_request

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
