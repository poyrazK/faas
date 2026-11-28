from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.environment_workload_source_kind import (
    EnvironmentWorkloadSourceKind,
    check_environment_workload_source_kind,
)
from ..models.environment_workload_source_runtime import (
    EnvironmentWorkloadSourceRuntime,
    check_environment_workload_source_runtime,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentWorkloadSource")


@_attrs_define
class EnvironmentWorkloadSource:
    """Build source within the approved Git tree, a function runner, or an immutable OCI digest. Function sources require
    runtime and exclude dockerfile. An immutable image may explicitly opt into full-rootfs dispatch. The gated internal
    executor reserves new private workloads when app is omitted. Preparation does not grant serving authority.

    """

    kind: EnvironmentWorkloadSourceKind
    reuse_unchanged_build: bool | Unset = False
    """Opt into isolated canonical Git context and same-app signed build reuse. A fresh target candidate and
    qualification are always required. An explicit build_artifact_id takes precedence. Link entries in the context
    are unsupported."""
    build_context: str | Unset = UNSET
    """Repository-relative context directory; defaults to the entire repository when reuse is enabled. Must contain
    source.directory and all shared build inputs. Files outside this directory are not delivered to the builder.
    Requires reuse_unchanged_build."""
    build_artifact_id: UUID | Unset = UNSET
    """Existing signed source-build producer. Requires build_artifact_hash; same mapped app only. Unsupported for
    images and scheduled Jobs."""
    build_artifact_hash: str | Unset = UNSET
    """Exact immutable producer input hash. Retention does not grant qualification or activation authority."""
    runtime: EnvironmentWorkloadSourceRuntime | Unset = UNSET
    """Supported runner; required only when kind is function."""
    directory: str | Unset = UNSET
    dockerfile: str | Unset = UNSET
    image: str | Unset = UNSET
    full_rootfs: bool | Unset = UNSET
    """Explicit full-rootfs dispatch for an immutable image source; unsupported for other source kinds."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        reuse_unchanged_build = self.reuse_unchanged_build

        build_context = self.build_context

        build_artifact_id: str | Unset = UNSET
        if not isinstance(self.build_artifact_id, Unset):
            build_artifact_id = str(self.build_artifact_id)

        build_artifact_hash = self.build_artifact_hash

        runtime: str | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime

        directory = self.directory

        dockerfile = self.dockerfile

        image = self.image

        full_rootfs = self.full_rootfs

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
            }
        )
        if reuse_unchanged_build is not UNSET:
            field_dict["reuse_unchanged_build"] = reuse_unchanged_build
        if build_context is not UNSET:
            field_dict["build_context"] = build_context
        if build_artifact_id is not UNSET:
            field_dict["build_artifact_id"] = build_artifact_id
        if build_artifact_hash is not UNSET:
            field_dict["build_artifact_hash"] = build_artifact_hash
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if directory is not UNSET:
            field_dict["directory"] = directory
        if dockerfile is not UNSET:
            field_dict["dockerfile"] = dockerfile
        if image is not UNSET:
            field_dict["image"] = image
        if full_rootfs is not UNSET:
            field_dict["full_rootfs"] = full_rootfs

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_environment_workload_source_kind(d.pop("kind"))

        reuse_unchanged_build = d.pop("reuse_unchanged_build", UNSET)

        build_context = d.pop("build_context", UNSET)

        _build_artifact_id = d.pop("build_artifact_id", UNSET)
        build_artifact_id: UUID | Unset
        if isinstance(_build_artifact_id, Unset):
            build_artifact_id = UNSET
        else:
            build_artifact_id = UUID(_build_artifact_id)

        build_artifact_hash = d.pop("build_artifact_hash", UNSET)

        _runtime = d.pop("runtime", UNSET)
        runtime: EnvironmentWorkloadSourceRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = check_environment_workload_source_runtime(_runtime)

        directory = d.pop("directory", UNSET)

        dockerfile = d.pop("dockerfile", UNSET)

        image = d.pop("image", UNSET)

        full_rootfs = d.pop("full_rootfs", UNSET)

        environment_workload_source = cls(
            kind=kind,
            reuse_unchanged_build=reuse_unchanged_build,
            build_context=build_context,
            build_artifact_id=build_artifact_id,
            build_artifact_hash=build_artifact_hash,
            runtime=runtime,
            directory=directory,
            dockerfile=dockerfile,
            image=image,
            full_rootfs=full_rootfs,
        )

        return environment_workload_source
