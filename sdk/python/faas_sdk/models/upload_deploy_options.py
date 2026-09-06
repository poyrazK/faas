from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_spec import WorkflowSpec


T = TypeVar("T", bound="UploadDeployOptions")


@_attrs_define
class UploadDeployOptions:
    """Deployment metadata persisted with the upload session and applied at commit."""

    runtime: str | Unset = UNSET
    handler: str | Unset = UNSET
    dockerfile: bool | Unset = UNSET
    source_root: str | Unset = UNSET
    reason: str | Unset = UNSET
    tag: str | Unset = UNSET
    deployed_by: str | Unset = UNSET
    pr_number: int | Unset = UNSET
    workflows: list[WorkflowSpec] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        runtime = self.runtime

        handler = self.handler

        dockerfile = self.dockerfile

        source_root = self.source_root

        reason = self.reason

        tag = self.tag

        deployed_by = self.deployed_by

        pr_number = self.pr_number

        workflows: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workflows, Unset):
            workflows = []
            for workflows_item_data in self.workflows:
                workflows_item = workflows_item_data.to_dict()
                workflows.append(workflows_item)

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if handler is not UNSET:
            field_dict["handler"] = handler
        if dockerfile is not UNSET:
            field_dict["dockerfile"] = dockerfile
        if source_root is not UNSET:
            field_dict["source_root"] = source_root
        if reason is not UNSET:
            field_dict["reason"] = reason
        if tag is not UNSET:
            field_dict["tag"] = tag
        if deployed_by is not UNSET:
            field_dict["deployed_by"] = deployed_by
        if pr_number is not UNSET:
            field_dict["pr_number"] = pr_number
        if workflows is not UNSET:
            field_dict["workflows"] = workflows

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_spec import WorkflowSpec

        d = dict(src_dict)
        runtime = d.pop("runtime", UNSET)

        handler = d.pop("handler", UNSET)

        dockerfile = d.pop("dockerfile", UNSET)

        source_root = d.pop("source_root", UNSET)

        reason = d.pop("reason", UNSET)

        tag = d.pop("tag", UNSET)

        deployed_by = d.pop("deployed_by", UNSET)

        pr_number = d.pop("pr_number", UNSET)

        _workflows = d.pop("workflows", UNSET)
        workflows: list[WorkflowSpec] | Unset = UNSET
        if _workflows is not UNSET:
            workflows = []
            for workflows_item_data in _workflows:
                workflows_item = WorkflowSpec.from_dict(workflows_item_data)

                workflows.append(workflows_item)

        upload_deploy_options = cls(
            runtime=runtime,
            handler=handler,
            dockerfile=dockerfile,
            source_root=source_root,
            reason=reason,
            tag=tag,
            deployed_by=deployed_by,
            pr_number=pr_number,
            workflows=workflows,
        )

        return upload_deploy_options
