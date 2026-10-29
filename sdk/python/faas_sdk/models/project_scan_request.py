from __future__ import annotations

import json
from collections.abc import Mapping
from io import BytesIO
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from .. import types
from ..types import UNSET, File, Unset

if TYPE_CHECKING:
    from ..models.project_scan_request_app_slugs import ProjectScanRequestAppSlugs


T = TypeVar("T", bound="ProjectScanRequest")


@_attrs_define
class ProjectScanRequest:
    """Multipart body for POST /v1/projects/scan (dry-run)."""

    source: File
    """tar.gz of the repo root."""
    release_config: str | Unset = UNSET
    """JSON-encoded ProjectReleaseConfigRequest for atomic-config-verify; contains only non-secret workload
    variables."""
    migration: bool | Unset = False
    """Preserve repository service identities and suggest project-prefixed app names. Requires no_triggers and no
    repository push binding; apply repeats the reviewed setting."""
    app_slugs: ProjectScanRequestAppSlugs | Unset = UNSET
    """Exact repository service names mapped to Gregale app names. Existing project members retain their names.
    Names are checked for format, reservation and availability; they are never allocated by scan. Multipart clients
    send this field as JSON."""
    project_slug: str | Unset = UNSET
    """kebab slug; default = repo dir basename"""
    repo_full_name: str | Unset = UNSET
    """GitHub owner/name to persist for push reconciliation"""
    production_branch: str | Unset = UNSET
    install_id: int | Unset = UNSET
    """GitHub installation id (with --repository or --repo); 0 for unbound repos"""
    only: str | Unset = UNSET
    """CSV of workload names to include (others skipped)"""
    environment: str | Unset = UNSET
    """Environment slug used to scope all discovered workloads"""
    no_triggers: bool | Unset = False
    """Leave trigger and async-route declarations and existing project trigger/route state unchanged for this
    scan/apply pair."""
    platform_tenant_required: bool | Unset = UNSET
    """Override the customer identity policy for selected workloads in this scan/apply pair (Hobby and above).
    Omitted uses Compose declarations and preserves existing app policy. Apply must repeat the same override as
    scan."""
    override_freeze: str | Unset = UNSET
    """Apply only: release frozen apps to production anyway; the reason is audited."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source = self.source.to_tuple()

        release_config = self.release_config

        migration = self.migration

        app_slugs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.app_slugs, Unset):
            app_slugs = self.app_slugs.to_dict()

        project_slug = self.project_slug

        repo_full_name = self.repo_full_name

        production_branch = self.production_branch

        install_id = self.install_id

        only = self.only

        environment = self.environment

        no_triggers = self.no_triggers

        platform_tenant_required = self.platform_tenant_required

        override_freeze = self.override_freeze

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
            }
        )
        if release_config is not UNSET:
            field_dict["release_config"] = release_config
        if migration is not UNSET:
            field_dict["migration"] = migration
        if app_slugs is not UNSET:
            field_dict["app_slugs"] = app_slugs
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
        if no_triggers is not UNSET:
            field_dict["no_triggers"] = no_triggers
        if platform_tenant_required is not UNSET:
            field_dict["platform_tenant_required"] = platform_tenant_required
        if override_freeze is not UNSET:
            field_dict["override_freeze"] = override_freeze

        return field_dict

    def to_multipart(self) -> types.RequestFiles:
        files: types.RequestFiles = []

        files.append(("source", self.source.to_tuple()))

        if not isinstance(self.release_config, Unset):
            files.append(("release_config", (None, str(self.release_config).encode(), "text/plain")))

        if not isinstance(self.migration, Unset):
            files.append(("migration", (None, str(self.migration).encode(), "text/plain")))

        if not isinstance(self.app_slugs, Unset):
            files.append(("app_slugs", (None, json.dumps(self.app_slugs.to_dict()).encode(), "application/json")))

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

        if not isinstance(self.no_triggers, Unset):
            files.append(("no_triggers", (None, str(self.no_triggers).encode(), "text/plain")))

        if not isinstance(self.platform_tenant_required, Unset):
            files.append(
                ("platform_tenant_required", (None, str(self.platform_tenant_required).encode(), "text/plain"))
            )

        if not isinstance(self.override_freeze, Unset):
            files.append(("override_freeze", (None, str(self.override_freeze).encode(), "text/plain")))

        for prop_name, prop in self.additional_properties.items():
            files.append((prop_name, (None, str(prop).encode(), "text/plain")))

        return files

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_scan_request_app_slugs import ProjectScanRequestAppSlugs

        d = dict(src_dict)
        source = File(payload=BytesIO(d.pop("source")))

        release_config = d.pop("release_config", UNSET)

        migration = d.pop("migration", UNSET)

        _app_slugs = d.pop("app_slugs", UNSET)
        app_slugs: ProjectScanRequestAppSlugs | Unset
        if isinstance(_app_slugs, Unset):
            app_slugs = UNSET
        else:
            app_slugs = ProjectScanRequestAppSlugs.from_dict(_app_slugs)

        project_slug = d.pop("project_slug", UNSET)

        repo_full_name = d.pop("repo_full_name", UNSET)

        production_branch = d.pop("production_branch", UNSET)

        install_id = d.pop("install_id", UNSET)

        only = d.pop("only", UNSET)

        environment = d.pop("environment", UNSET)

        no_triggers = d.pop("no_triggers", UNSET)

        platform_tenant_required = d.pop("platform_tenant_required", UNSET)

        override_freeze = d.pop("override_freeze", UNSET)

        project_scan_request = cls(
            source=source,
            release_config=release_config,
            migration=migration,
            app_slugs=app_slugs,
            project_slug=project_slug,
            repo_full_name=repo_full_name,
            production_branch=production_branch,
            install_id=install_id,
            only=only,
            environment=environment,
            no_triggers=no_triggers,
            platform_tenant_required=platform_tenant_required,
            override_freeze=override_freeze,
        )

        project_scan_request.additional_properties = d
        return project_scan_request

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
