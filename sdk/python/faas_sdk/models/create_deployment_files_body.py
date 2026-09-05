from __future__ import annotations

from collections.abc import Mapping
from io import BytesIO
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from .. import types
from ..models.create_deployment_files_body_kind import (
    CreateDeploymentFilesBodyKind,
    check_create_deployment_files_body_kind,
)
from ..models.create_deployment_files_body_runtime import (
    CreateDeploymentFilesBodyRuntime,
    check_create_deployment_files_body_runtime,
)
from ..types import UNSET, File, FileTypes, Unset

T = TypeVar("T", bound="CreateDeploymentFilesBody")


@_attrs_define
class CreateDeploymentFilesBody:
    source: File | Unset = UNSET
    """Multipart file field — the source tarball or Dockerfile context."""
    dockerfile: bool | Unset = UNSET
    kind: CreateDeploymentFilesBodyKind | Unset = UNSET
    runtime: CreateDeploymentFilesBodyRuntime | Unset = UNSET
    source_root: str | Unset = UNSET
    """Optional repository-relative directory to build from when source contains a workspace context. Empty or
    omitted means the archive root."""
    workflows: str | Unset = UNSET
    """JSON array of workflow definitions (plan-gated)."""
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

        source_root = self.source_root

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
        if source_root is not UNSET:
            field_dict["source_root"] = source_root
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

        if not isinstance(self.source_root, Unset):
            files.append(("source_root", (None, str(self.source_root).encode(), "text/plain")))

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
        kind: CreateDeploymentFilesBodyKind | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_create_deployment_files_body_kind(_kind)

        _runtime = d.pop("runtime", UNSET)
        runtime: CreateDeploymentFilesBodyRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = check_create_deployment_files_body_runtime(_runtime)

        source_root = d.pop("source_root", UNSET)

        workflows = d.pop("workflows", UNSET)

        create_deployment_files_body = cls(
            source=source,
            dockerfile=dockerfile,
            kind=kind,
            runtime=runtime,
            source_root=source_root,
            workflows=workflows,
        )

        create_deployment_files_body.additional_properties = d
        return create_deployment_files_body

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
