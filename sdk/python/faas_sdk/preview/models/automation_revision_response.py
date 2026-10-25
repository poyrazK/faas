from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_check_evidence import AutomationCheckEvidence
    from ..models.workflow_spec import WorkflowSpec


T = TypeVar("T", bound="AutomationRevisionResponse")


@_attrs_define
class AutomationRevisionResponse:
    """Immutable published automation definition with actor and rollout provenance."""

    version: int
    """Immutable published revision identifier."""
    definition: WorkflowSpec
    """A named workflow DAG submitted with a deployment (ADR-081). max_concurrent_runs caps active run instances
    for this workflow; excess admitted runs remain pending until a slot opens, subject to the app plan's run quota.
    max_concurrent_actions caps active executor steps across runs of this workflow; steps wait in the scheduler
    queue while all action slots are occupied."""
    definition_hash: str
    """SHA-256 of the canonical JSON encoding of definition."""
    recorded_at: datetime.datetime
    """When this immutable history record was stored; for legacy snapshots this is the migration time."""
    legacy_snapshot: bool
    """True for the one current publication copied into history during rollout; older history was not retained."""
    published_by_account_id: UUID
    """Account that published this revision, or owned the legacy snapshot at rollout."""
    check_evidence: AutomationCheckEvidence | Unset = UNSET
    """Simulation check metadata. server_verified is set only for server-issued publishing receipts; legacy or
    plain client evidence is unverified. Bound to the saved draft hash and version. Contains metadata only; no
    sample inputs, outputs, or failure text. Checked time must be within the past day (five minutes of future clock
    skew allowed)."""
    published_by_api_key_id: UUID | Unset = UNSET
    """API key used for the publish when the request used key authentication; omitted for session authentication
    and legacy snapshots."""

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        definition = self.definition.to_dict()

        definition_hash = self.definition_hash

        recorded_at = self.recorded_at.isoformat()

        legacy_snapshot = self.legacy_snapshot

        published_by_account_id = str(self.published_by_account_id)

        check_evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.check_evidence, Unset):
            check_evidence = self.check_evidence.to_dict()

        published_by_api_key_id: str | Unset = UNSET
        if not isinstance(self.published_by_api_key_id, Unset):
            published_by_api_key_id = str(self.published_by_api_key_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "definition": definition,
                "definition_hash": definition_hash,
                "recorded_at": recorded_at,
                "legacy_snapshot": legacy_snapshot,
                "published_by_account_id": published_by_account_id,
            }
        )
        if check_evidence is not UNSET:
            field_dict["check_evidence"] = check_evidence
        if published_by_api_key_id is not UNSET:
            field_dict["published_by_api_key_id"] = published_by_api_key_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_check_evidence import AutomationCheckEvidence
        from ..models.workflow_spec import WorkflowSpec

        d = dict(src_dict)
        version = d.pop("version")

        definition = WorkflowSpec.from_dict(d.pop("definition"))

        definition_hash = d.pop("definition_hash")

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        legacy_snapshot = d.pop("legacy_snapshot")

        published_by_account_id = UUID(d.pop("published_by_account_id"))

        _check_evidence = d.pop("check_evidence", UNSET)
        check_evidence: AutomationCheckEvidence | Unset
        if isinstance(_check_evidence, Unset):
            check_evidence = UNSET
        else:
            check_evidence = AutomationCheckEvidence.from_dict(_check_evidence)

        _published_by_api_key_id = d.pop("published_by_api_key_id", UNSET)
        published_by_api_key_id: UUID | Unset
        if isinstance(_published_by_api_key_id, Unset):
            published_by_api_key_id = UNSET
        else:
            published_by_api_key_id = UUID(_published_by_api_key_id)

        automation_revision_response = cls(
            version=version,
            definition=definition,
            definition_hash=definition_hash,
            recorded_at=recorded_at,
            legacy_snapshot=legacy_snapshot,
            published_by_account_id=published_by_account_id,
            check_evidence=check_evidence,
            published_by_api_key_id=published_by_api_key_id,
        )

        return automation_revision_response
