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
    from ..models.project_apply_request_app_slugs import ProjectApplyRequestAppSlugs
    from ..models.project_initial_secrets import ProjectInitialSecrets


T = TypeVar("T", bound="ProjectApplyRequest")


@_attrs_define
class ProjectApplyRequest:
    """Multipart body for POST /v1/projects (apply). Same shape as
    ProjectScanRequest; the apply handler resolves AppIDs and
    inserts crons in a follow-up pass.

    """

    source: File
    release_config: str | Unset = UNSET
    """Planned non-secret workload variables encoded as ProjectReleaseConfigRequest JSON for atomic-config-verify
    admission."""
    migration: bool | Unset = False
    """Apply the reviewed migration naming mode, preserving repository service identities and project-prefixed app
    allocations. Automatic triggers and repository push bindings must be disabled."""
    app_slugs: ProjectApplyRequestAppSlugs | Unset = UNSET
    """Reviewed repository service-to-app allocations for this apply. Preserve existing project app names and
    repeat the exact selected names from review; multipart clients encode this object as JSON."""
    project_slug: str | Unset = UNSET
    repo_full_name: str | Unset = UNSET
    production_branch: str | Unset = UNSET
    install_id: int | Unset = UNSET
    only: str | Unset = UNSET
    environment: str | Unset = UNSET
    """Environment slug applied to deployments created by this apply"""
    secrets: str | Unset = UNSET
    """JSON object of secret names to plaintext values, sealed for every selected workload in the deployment
    environment before any build is queued. Requires secrets:write or admin in addition to deploy permission. Uses
    the existing plan secret count/value quotas; never included in source or responses. Failed configuration queues
    no builds; already applied configuration remains available for retry."""
    approval_token: str | Unset = UNSET
    """Short-lived approval credential for the exact protected-environment plan"""
    no_triggers: bool | Unset = False
    """Leave trigger and async-route declarations and existing project trigger/route state unchanged for this
    apply."""
    platform_tenant_required: bool | Unset = UNSET
    """Override the customer identity policy for selected workloads (Hobby and above). Must match the scan
    override. Omitted uses Compose declarations and preserves existing app policy."""
    initial_secrets: ProjectInitialSecrets | Unset = UNSET
    """Request-only mapping of exact selected workload names to initial sealed environment values. Values stay in
    memory until sealing and never enter responses or source caches."""
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

        secrets = self.secrets

        approval_token = self.approval_token

        no_triggers = self.no_triggers

        platform_tenant_required = self.platform_tenant_required

        initial_secrets: dict[str, Any] | Unset = UNSET
        if not isinstance(self.initial_secrets, Unset):
            initial_secrets = self.initial_secrets.to_dict()

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
        if secrets is not UNSET:
            field_dict["secrets"] = secrets
        if approval_token is not UNSET:
            field_dict["approval_token"] = approval_token
        if no_triggers is not UNSET:
            field_dict["no_triggers"] = no_triggers
        if platform_tenant_required is not UNSET:
            field_dict["platform_tenant_required"] = platform_tenant_required
        if initial_secrets is not UNSET:
            field_dict["initial_secrets"] = initial_secrets

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

        if not isinstance(self.secrets, Unset):
            files.append(("secrets", (None, str(self.secrets).encode(), "text/plain")))

        if not isinstance(self.approval_token, Unset):
            files.append(("approval_token", (None, str(self.approval_token).encode(), "text/plain")))

        if not isinstance(self.no_triggers, Unset):
            files.append(("no_triggers", (None, str(self.no_triggers).encode(), "text/plain")))

        if not isinstance(self.platform_tenant_required, Unset):
            files.append(
                ("platform_tenant_required", (None, str(self.platform_tenant_required).encode(), "text/plain"))
            )

        if not isinstance(self.initial_secrets, Unset):
            files.append(
                ("initial_secrets", (None, json.dumps(self.initial_secrets.to_dict()).encode(), "application/json"))
            )

        for prop_name, prop in self.additional_properties.items():
            files.append((prop_name, (None, str(prop).encode(), "text/plain")))

        return files

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_apply_request_app_slugs import ProjectApplyRequestAppSlugs
        from ..models.project_initial_secrets import ProjectInitialSecrets

        d = dict(src_dict)
        source = File(payload=BytesIO(d.pop("source")))

        release_config = d.pop("release_config", UNSET)

        migration = d.pop("migration", UNSET)

        _app_slugs = d.pop("app_slugs", UNSET)
        app_slugs: ProjectApplyRequestAppSlugs | Unset
        if isinstance(_app_slugs, Unset):
            app_slugs = UNSET
        else:
            app_slugs = ProjectApplyRequestAppSlugs.from_dict(_app_slugs)

        project_slug = d.pop("project_slug", UNSET)

        repo_full_name = d.pop("repo_full_name", UNSET)

        production_branch = d.pop("production_branch", UNSET)

        install_id = d.pop("install_id", UNSET)

        only = d.pop("only", UNSET)

        environment = d.pop("environment", UNSET)

        secrets = d.pop("secrets", UNSET)

        approval_token = d.pop("approval_token", UNSET)

        no_triggers = d.pop("no_triggers", UNSET)

        platform_tenant_required = d.pop("platform_tenant_required", UNSET)

        _initial_secrets = d.pop("initial_secrets", UNSET)
        initial_secrets: ProjectInitialSecrets | Unset
        if isinstance(_initial_secrets, Unset):
            initial_secrets = UNSET
        else:
            initial_secrets = ProjectInitialSecrets.from_dict(_initial_secrets)

        project_apply_request = cls(
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
            secrets=secrets,
            approval_token=approval_token,
            no_triggers=no_triggers,
            platform_tenant_required=platform_tenant_required,
            initial_secrets=initial_secrets,
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
