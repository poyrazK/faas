from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_match_kind import (
    OperationWorkflowPlaybookMatchKind,
    check_operation_workflow_playbook_match_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPlaybook")


@_attrs_define
class OperationWorkflowPlaybook:
    """OperationWorkflowPlaybook. Application-owned advisory guidance; evidence requirements are human checklists and links
    are never executed by Gregale.

    """

    id: str
    """OperationWorkflowPlaybook.id. Unique bounded lowercase identifier within the workflow contract."""
    version: int
    """OperationWorkflowPlaybook.version. Explicit playbook revision, pinned alongside the workflow contract
    version."""
    match_kind: OperationWorkflowPlaybookMatchKind
    """OperationWorkflowPlaybook.match_kind. Exact observed fact category."""
    match_code: str
    """OperationWorkflowPlaybook.match_code. Exact blocker code, milestone, dependency workflow, verification
    blocker code or readiness reason code."""
    summary: str
    """OperationWorkflowPlaybook.summary. Application-authored guidance without control characters or surrounding
    whitespace."""
    owner: str | Unset = UNSET
    """OperationWorkflowPlaybook.owner. Suggested responsible team; does not assign or override coordination
    ownership."""
    required_evidence: list[str] | Unset = UNSET
    """OperationWorkflowPlaybook.required_evidence. Advisory evidence checklist, not automatically verified
    prerequisites."""
    runbook_url: str | Unset = UNSET
    """OperationWorkflowPlaybook.runbook_url. Absolute HTTPS runbook link without user credentials; never fetched
    automatically."""
    action_url: str | Unset = UNSET
    """OperationWorkflowPlaybook.action_url. Absolute HTTPS application action link without user credentials;
    application handles authorization and execution."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        version = self.version

        match_kind: str = self.match_kind

        match_code = self.match_code

        summary = self.summary

        owner = self.owner

        required_evidence: list[str] | Unset = UNSET
        if not isinstance(self.required_evidence, Unset):
            required_evidence = self.required_evidence

        runbook_url = self.runbook_url

        action_url = self.action_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "version": version,
                "match_kind": match_kind,
                "match_code": match_code,
                "summary": summary,
            }
        )
        if owner is not UNSET:
            field_dict["owner"] = owner
        if required_evidence is not UNSET:
            field_dict["required_evidence"] = required_evidence
        if runbook_url is not UNSET:
            field_dict["runbook_url"] = runbook_url
        if action_url is not UNSET:
            field_dict["action_url"] = action_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        version = d.pop("version")

        match_kind = check_operation_workflow_playbook_match_kind(d.pop("match_kind"))

        match_code = d.pop("match_code")

        summary = d.pop("summary")

        owner = d.pop("owner", UNSET)

        required_evidence = cast(list[str], d.pop("required_evidence", UNSET))

        runbook_url = d.pop("runbook_url", UNSET)

        action_url = d.pop("action_url", UNSET)

        operation_workflow_playbook = cls(
            id=id,
            version=version,
            match_kind=match_kind,
            match_code=match_code,
            summary=summary,
            owner=owner,
            required_evidence=required_evidence,
            runbook_url=runbook_url,
            action_url=action_url,
        )

        return operation_workflow_playbook
