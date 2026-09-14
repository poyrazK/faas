from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectSourceRefScanRequest")


@_attrs_define
class ProjectSourceRefScanRequest:
    """Connected GitHub repository input for POST /v1/projects/scan/source-ref."""

    repo: str
    """GitHub owner/name to fetch through the connected installation."""
    ref: str
    """Branch, tag, or commit ref to scan."""
    project_slug: str
    repo_full_name: str | Unset = UNSET
    """Repository binding stored if the plan is later applied; defaults to repo."""
    production_branch: str | Unset = "main"
    install_id: int | Unset = UNSET
    """Optional connected installation id. Omit to resolve the single installation that can access repo."""
    only: list[str] | Unset = UNSET
    exclude: list[str] | Unset = UNSET
    no_triggers: bool | Unset = False
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        repo = self.repo

        ref = self.ref

        project_slug = self.project_slug

        repo_full_name = self.repo_full_name

        production_branch = self.production_branch

        install_id = self.install_id

        only: list[str] | Unset = UNSET
        if not isinstance(self.only, Unset):
            only = self.only

        exclude: list[str] | Unset = UNSET
        if not isinstance(self.exclude, Unset):
            exclude = self.exclude

        no_triggers = self.no_triggers

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "repo": repo,
                "ref": ref,
                "project_slug": project_slug,
            }
        )
        if repo_full_name is not UNSET:
            field_dict["repo_full_name"] = repo_full_name
        if production_branch is not UNSET:
            field_dict["production_branch"] = production_branch
        if install_id is not UNSET:
            field_dict["install_id"] = install_id
        if only is not UNSET:
            field_dict["only"] = only
        if exclude is not UNSET:
            field_dict["exclude"] = exclude
        if no_triggers is not UNSET:
            field_dict["no_triggers"] = no_triggers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        repo = d.pop("repo")

        ref = d.pop("ref")

        project_slug = d.pop("project_slug")

        repo_full_name = d.pop("repo_full_name", UNSET)

        production_branch = d.pop("production_branch", UNSET)

        install_id = d.pop("install_id", UNSET)

        only = cast(list[str], d.pop("only", UNSET))

        exclude = cast(list[str], d.pop("exclude", UNSET))

        no_triggers = d.pop("no_triggers", UNSET)

        project_source_ref_scan_request = cls(
            repo=repo,
            ref=ref,
            project_slug=project_slug,
            repo_full_name=repo_full_name,
            production_branch=production_branch,
            install_id=install_id,
            only=only,
            exclude=exclude,
            no_triggers=no_triggers,
        )

        project_source_ref_scan_request.additional_properties = d
        return project_source_ref_scan_request

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
