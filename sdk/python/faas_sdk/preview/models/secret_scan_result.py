from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.secret_finding import SecretFinding


T = TypeVar("T", bound="SecretScanResult")


@_attrs_define
class SecretScanResult:
    """Customer-facing wire shape of one deployment's secret-scan
    audit row. Mirrors the `ScanResult` shape but for the
    secret-scan pipeline. The `status` enum is the closed set
    the imaged-side writer (PR-A) stamps: `complete` for a
    clean walk, `complete_with_redactions` for a hit (mirrors
    the v2 widening on `deployments_scan_status_chk` from
    migration 00264). `image_digest` is the OCI digest the
    scan ran against (PR-A) — mirrors
    `ScanResult.image_digest` so a side-by-side compare
    renders both scans against the same bytes. `findings[]`
    may be empty (clean walk); the field is always present
    for round-trip JSON stability.

    """

    status: str
    findings: list[SecretFinding]
    scanned_at: datetime.datetime | Unset = UNSET
    """RFC 3339 UTC. Empty when the deployment hasn't been
    secret-scanned yet.
    """
    image_digest: str | Unset = UNSET
    """OCI digest the scan ran against (PR-A). Mirrors
    `ScanResult.image_digest` for cross-pipeline
    comparison. Empty for pre-PR-A rows.
    """
    error: str | Unset = UNSET
    """In-band explanation when the audit row is unreadable
    (jsonb decode failed). Mirrors `ScanResult.error`.
    Empty on the success path — `status` carries the
    closed-set signal there.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status = self.status

        findings = []
        for findings_item_data in self.findings:
            findings_item = findings_item_data.to_dict()
            findings.append(findings_item)

        scanned_at: str | Unset = UNSET
        if not isinstance(self.scanned_at, Unset):
            scanned_at = self.scanned_at.isoformat()

        image_digest = self.image_digest

        error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "findings": findings,
            }
        )
        if scanned_at is not UNSET:
            field_dict["scanned_at"] = scanned_at
        if image_digest is not UNSET:
            field_dict["image_digest"] = image_digest
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.secret_finding import SecretFinding

        d = dict(src_dict)
        status = d.pop("status")

        findings = []
        _findings = d.pop("findings")
        for findings_item_data in _findings:
            findings_item = SecretFinding.from_dict(findings_item_data)

            findings.append(findings_item)

        _scanned_at = d.pop("scanned_at", UNSET)
        scanned_at: datetime.datetime | Unset
        if isinstance(_scanned_at, Unset):
            scanned_at = UNSET
        else:
            scanned_at = datetime.datetime.fromisoformat(_scanned_at)

        image_digest = d.pop("image_digest", UNSET)

        error = d.pop("error", UNSET)

        secret_scan_result = cls(
            status=status,
            findings=findings,
            scanned_at=scanned_at,
            image_digest=image_digest,
            error=error,
        )

        secret_scan_result.additional_properties = d
        return secret_scan_result

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
