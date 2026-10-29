from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_usage_import_window import ManagedPostgresUsageImportWindow


T = TypeVar("T", bound="ManagedPostgresUsageImportRequest")


@_attrs_define
class ManagedPostgresUsageImportRequest:
    """Operator-attested retained usage for a known independently accounted database."""

    import_id: UUID
    """Durable request identity; preserve across retries."""
    database_id: UUID
    """Independently accounted database; shared restores must import against their root."""
    evidence_reference: str
    """Non-secret retained evidence locator; never a credential or signed URL."""
    evidence_sha256: str
    """SHA-256 of the retained source artifact, attested by the operator; no artifact is fetched by this API."""
    reason: str
    windows: list[ManagedPostgresUsageImportWindow]
    """Ascending contiguous complete policy-sized windows with exactly the backend's advertised meters. Costs are
    calculated from the current server policy. Maximum request size 1 MiB."""
    expected_revision: str | Unset = UNSET
    """Omit for preview; required for apply."""

    def to_dict(self) -> dict[str, Any]:
        import_id = str(self.import_id)

        database_id = str(self.database_id)

        evidence_reference = self.evidence_reference

        evidence_sha256 = self.evidence_sha256

        reason = self.reason

        windows = []
        for windows_item_data in self.windows:
            windows_item = windows_item_data.to_dict()
            windows.append(windows_item)

        expected_revision = self.expected_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "import_id": import_id,
                "database_id": database_id,
                "evidence_reference": evidence_reference,
                "evidence_sha256": evidence_sha256,
                "reason": reason,
                "windows": windows,
            }
        )
        if expected_revision is not UNSET:
            field_dict["expected_revision"] = expected_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_usage_import_window import ManagedPostgresUsageImportWindow

        d = dict(src_dict)
        import_id = UUID(d.pop("import_id"))

        database_id = UUID(d.pop("database_id"))

        evidence_reference = d.pop("evidence_reference")

        evidence_sha256 = d.pop("evidence_sha256")

        reason = d.pop("reason")

        windows = []
        _windows = d.pop("windows")
        for windows_item_data in _windows:
            windows_item = ManagedPostgresUsageImportWindow.from_dict(windows_item_data)

            windows.append(windows_item)

        expected_revision = d.pop("expected_revision", UNSET)

        managed_postgres_usage_import_request = cls(
            import_id=import_id,
            database_id=database_id,
            evidence_reference=evidence_reference,
            evidence_sha256=evidence_sha256,
            reason=reason,
            windows=windows,
            expected_revision=expected_revision,
        )

        return managed_postgres_usage_import_request
