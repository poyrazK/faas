from __future__ import annotations

from collections.abc import Mapping
from io import BytesIO
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from .. import types
from ..models.deploy_dev_source_body_runtime import DeployDevSourceBodyRuntime, check_deploy_dev_source_body_runtime
from ..types import UNSET, File, Unset

T = TypeVar("T", bound="DeployDevSourceBody")


@_attrs_define
class DeployDevSourceBody:
    source: File
    """Complete tar.gz when dev_source_base is absent; otherwise a tar.gz of changed entries."""
    dev_source_target: str
    """Canonical revision of the complete source tree after reconstruction."""
    dev_source_base: str | Unset = UNSET
    """Canonical cached source revision. Omit for a complete snapshot."""
    dev_source_deleted: str | Unset = UNSET
    """JSON string array of canonical archive paths removed since dev_source_base."""
    dockerfile: bool | Unset = UNSET
    runtime: DeployDevSourceBodyRuntime | Unset = UNSET
    handler: str | Unset = UNSET
    source_root: str | Unset = UNSET
    workflows: str | Unset = UNSET
    """Optional JSON workflow-definition array attached to this developer deployment."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source = self.source.to_tuple()

        dev_source_target = self.dev_source_target

        dev_source_base = self.dev_source_base

        dev_source_deleted = self.dev_source_deleted

        dockerfile = self.dockerfile

        runtime: str | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime

        handler = self.handler

        source_root = self.source_root

        workflows = self.workflows

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "dev_source_target": dev_source_target,
            }
        )
        if dev_source_base is not UNSET:
            field_dict["dev_source_base"] = dev_source_base
        if dev_source_deleted is not UNSET:
            field_dict["dev_source_deleted"] = dev_source_deleted
        if dockerfile is not UNSET:
            field_dict["dockerfile"] = dockerfile
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if handler is not UNSET:
            field_dict["handler"] = handler
        if source_root is not UNSET:
            field_dict["source_root"] = source_root
        if workflows is not UNSET:
            field_dict["workflows"] = workflows

        return field_dict

    def to_multipart(self) -> types.RequestFiles:
        files: types.RequestFiles = []

        files.append(("source", self.source.to_tuple()))

        files.append(("dev_source_target", (None, str(self.dev_source_target).encode(), "text/plain")))

        if not isinstance(self.dev_source_base, Unset):
            files.append(("dev_source_base", (None, str(self.dev_source_base).encode(), "text/plain")))

        if not isinstance(self.dev_source_deleted, Unset):
            files.append(("dev_source_deleted", (None, str(self.dev_source_deleted).encode(), "text/plain")))

        if not isinstance(self.dockerfile, Unset):
            files.append(("dockerfile", (None, str(self.dockerfile).encode(), "text/plain")))

        if not isinstance(self.runtime, Unset):
            files.append(("runtime", (None, str(self.runtime).encode(), "text/plain")))

        if not isinstance(self.handler, Unset):
            files.append(("handler", (None, str(self.handler).encode(), "text/plain")))

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
        source = File(payload=BytesIO(d.pop("source")))

        dev_source_target = d.pop("dev_source_target")

        dev_source_base = d.pop("dev_source_base", UNSET)

        dev_source_deleted = d.pop("dev_source_deleted", UNSET)

        dockerfile = d.pop("dockerfile", UNSET)

        _runtime = d.pop("runtime", UNSET)
        runtime: DeployDevSourceBodyRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = check_deploy_dev_source_body_runtime(_runtime)

        handler = d.pop("handler", UNSET)

        source_root = d.pop("source_root", UNSET)

        workflows = d.pop("workflows", UNSET)

        deploy_dev_source_body = cls(
            source=source,
            dev_source_target=dev_source_target,
            dev_source_base=dev_source_base,
            dev_source_deleted=dev_source_deleted,
            dockerfile=dockerfile,
            runtime=runtime,
            handler=handler,
            source_root=source_root,
            workflows=workflows,
        )

        deploy_dev_source_body.additional_properties = d
        return deploy_dev_source_body

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
