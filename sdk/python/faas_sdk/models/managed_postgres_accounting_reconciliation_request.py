from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresAccountingReconciliationRequest")


@_attrs_define
class ManagedPostgresAccountingReconciliationRequest:
    """Operator-attested immutable backend identity and actual provider shutdown for one unresolved legacy tombstone. The
    operator verifies artifact ownership and lineage; the server does not fetch or authenticate the source. Missing
    lookup results are not shutdown evidence.

    """

    reconciliation_id: UUID
    """Durable reconciliation identity."""
    database_id: UUID
    """Deleted accountable database with no provider identity."""
    backend_id: str
    """Exact immutable catalog backend ID."""
    backend_fingerprint: str
    """Exact immutable catalog backend fingerprint."""
    provider_resource_id: str
    """Verified opaque provider identity; include branch identity where required by the adapter."""
    shutdown_at: datetime.datetime
    """Actual confirmed provider shutdown at or after catalog creation; microsecond precision."""
    observed_at: datetime.datetime
    """Actual evidence observation at or after shutdown and retained ledger observations; no future timestamps."""
    evidence_reference: str
    """Retained nonsecret artifact reference; no credentials or signed URLs."""
    evidence_sha256: str
    """SHA-256 of the operator-verified source artifact."""
    reason: str
    """Audited reason for reconciliation."""
    expected_revision: str | Unset = UNSET
    """Omit for preview; required for apply using the returned revision."""

    def to_dict(self) -> dict[str, Any]:
        reconciliation_id = str(self.reconciliation_id)

        database_id = str(self.database_id)

        backend_id = self.backend_id

        backend_fingerprint = self.backend_fingerprint

        provider_resource_id = self.provider_resource_id

        shutdown_at = self.shutdown_at.isoformat()

        observed_at = self.observed_at.isoformat()

        evidence_reference = self.evidence_reference

        evidence_sha256 = self.evidence_sha256

        reason = self.reason

        expected_revision = self.expected_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "reconciliation_id": reconciliation_id,
                "database_id": database_id,
                "backend_id": backend_id,
                "backend_fingerprint": backend_fingerprint,
                "provider_resource_id": provider_resource_id,
                "shutdown_at": shutdown_at,
                "observed_at": observed_at,
                "evidence_reference": evidence_reference,
                "evidence_sha256": evidence_sha256,
                "reason": reason,
            }
        )
        if expected_revision is not UNSET:
            field_dict["expected_revision"] = expected_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reconciliation_id = UUID(d.pop("reconciliation_id"))

        database_id = UUID(d.pop("database_id"))

        backend_id = d.pop("backend_id")

        backend_fingerprint = d.pop("backend_fingerprint")

        provider_resource_id = d.pop("provider_resource_id")

        shutdown_at = datetime.datetime.fromisoformat(d.pop("shutdown_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        evidence_reference = d.pop("evidence_reference")

        evidence_sha256 = d.pop("evidence_sha256")

        reason = d.pop("reason")

        expected_revision = d.pop("expected_revision", UNSET)

        managed_postgres_accounting_reconciliation_request = cls(
            reconciliation_id=reconciliation_id,
            database_id=database_id,
            backend_id=backend_id,
            backend_fingerprint=backend_fingerprint,
            provider_resource_id=provider_resource_id,
            shutdown_at=shutdown_at,
            observed_at=observed_at,
            evidence_reference=evidence_reference,
            evidence_sha256=evidence_sha256,
            reason=reason,
            expected_revision=expected_revision,
        )

        return managed_postgres_accounting_reconciliation_request
