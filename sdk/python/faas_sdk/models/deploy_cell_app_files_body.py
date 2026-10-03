from __future__ import annotations

from collections.abc import Mapping
from io import BytesIO
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from .. import types
from ..models.deploy_cell_app_files_body_kind import DeployCellAppFilesBodyKind, check_deploy_cell_app_files_body_kind
from ..models.deploy_cell_app_files_body_runtime import (
    DeployCellAppFilesBodyRuntime,
    check_deploy_cell_app_files_body_runtime,
)
from ..types import UNSET, File, FileTypes, Unset

T = TypeVar("T", bound="DeployCellAppFilesBody")


@_attrs_define
class DeployCellAppFilesBody:
    source: File | Unset = UNSET
    """Source archive passed to this cell’s existing tarball or Dockerfile build pipeline."""
    dockerfile: bool | Unset = UNSET
    kind: DeployCellAppFilesBodyKind | Unset = UNSET
    runtime: DeployCellAppFilesBodyRuntime | Unset = UNSET
    handler: str | Unset = UNSET
    """Function handler selector when a function runtime is supplied."""
    healthcheck: str | Unset = UNSET
    """Startup readiness configuration encoded as JSON, selecting either an HTTP path or a gRPC health check."""
    source_root: str | Unset = UNSET
    """Build directory relative to the uploaded regional archive. An omitted or empty directory selects its root."""
    scope: str | Unset = UNSET
    """Regional environment scope for this deployment; an omitted scope selects default."""
    source_url: str | Unset = UNSET
    """Repository URL recorded as regional deployment provenance. Code comes from the uploaded archive; this URL is
    not fetched."""
    commit_sha: str | Unset = UNSET
    """Lowercase hexadecimal Git revision recorded with this regional deployment."""
    workflows: str | Unset = UNSET
    """Workflow definitions encoded as a JSON array, subject to the regional account plan."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: FileTypes | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source.to_tuple()

        dockerfile = self.dockerfile

        kind: str | Unset = UNSET
        if not isinstance(self.kind, Unset):
            kind = self.kind

        runtime: str | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime

        handler = self.handler

        healthcheck = self.healthcheck

        source_root = self.source_root

        scope = self.scope

        source_url = self.source_url

        commit_sha = self.commit_sha

        workflows = self.workflows

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if source is not UNSET:
            field_dict["source"] = source
        if dockerfile is not UNSET:
            field_dict["dockerfile"] = dockerfile
        if kind is not UNSET:
            field_dict["kind"] = kind
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if handler is not UNSET:
            field_dict["handler"] = handler
        if healthcheck is not UNSET:
            field_dict["healthcheck"] = healthcheck
        if source_root is not UNSET:
            field_dict["source_root"] = source_root
        if scope is not UNSET:
            field_dict["scope"] = scope
        if source_url is not UNSET:
            field_dict["source_url"] = source_url
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if workflows is not UNSET:
            field_dict["workflows"] = workflows

        return field_dict

    def to_multipart(self) -> types.RequestFiles:
        files: types.RequestFiles = []

        if not isinstance(self.source, Unset):
            files.append(("source", self.source.to_tuple()))

        if not isinstance(self.dockerfile, Unset):
            files.append(("dockerfile", (None, str(self.dockerfile).encode(), "text/plain")))

        if not isinstance(self.kind, Unset):
            files.append(("kind", (None, str(self.kind).encode(), "text/plain")))

        if not isinstance(self.runtime, Unset):
            files.append(("runtime", (None, str(self.runtime).encode(), "text/plain")))

        if not isinstance(self.handler, Unset):
            files.append(("handler", (None, str(self.handler).encode(), "text/plain")))

        if not isinstance(self.healthcheck, Unset):
            files.append(("healthcheck", (None, str(self.healthcheck).encode(), "text/plain")))

        if not isinstance(self.source_root, Unset):
            files.append(("source_root", (None, str(self.source_root).encode(), "text/plain")))

        if not isinstance(self.scope, Unset):
            files.append(("scope", (None, str(self.scope).encode(), "text/plain")))

        if not isinstance(self.source_url, Unset):
            files.append(("source_url", (None, str(self.source_url).encode(), "text/plain")))

        if not isinstance(self.commit_sha, Unset):
            files.append(("commit_sha", (None, str(self.commit_sha).encode(), "text/plain")))

        if not isinstance(self.workflows, Unset):
            files.append(("workflows", (None, str(self.workflows).encode(), "text/plain")))

        for prop_name, prop in self.additional_properties.items():
            files.append((prop_name, (None, str(prop).encode(), "text/plain")))

        return files

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _source = d.pop("source", UNSET)
        source: File | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = File(payload=BytesIO(_source))

        dockerfile = d.pop("dockerfile", UNSET)

        _kind = d.pop("kind", UNSET)
        kind: DeployCellAppFilesBodyKind | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_deploy_cell_app_files_body_kind(_kind)

        _runtime = d.pop("runtime", UNSET)
        runtime: DeployCellAppFilesBodyRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = check_deploy_cell_app_files_body_runtime(_runtime)

        handler = d.pop("handler", UNSET)

        healthcheck = d.pop("healthcheck", UNSET)

        source_root = d.pop("source_root", UNSET)

        scope = d.pop("scope", UNSET)

        source_url = d.pop("source_url", UNSET)

        commit_sha = d.pop("commit_sha", UNSET)

        workflows = d.pop("workflows", UNSET)

        deploy_cell_app_files_body = cls(
            source=source,
            dockerfile=dockerfile,
            kind=kind,
            runtime=runtime,
            handler=handler,
            healthcheck=healthcheck,
            source_root=source_root,
            scope=scope,
            source_url=source_url,
            commit_sha=commit_sha,
            workflows=workflows,
        )

        deploy_cell_app_files_body.additional_properties = d
        return deploy_cell_app_files_body

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
