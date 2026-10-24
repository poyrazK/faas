from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.secret_finding_severity import SecretFindingSeverity, check_secret_finding_severity
from ..types import UNSET, Unset

T = TypeVar("T", bound="SecretFinding")


@_attrs_define
class SecretFinding:
    """Per-line entry of `Problem.secret_findings` AND
    `SecretScanResult.findings` (issue #862 + secret-scan v2,
    PR #873; PR-A 101 extends the surfaced shape via the new
    `layer` field). The shape mirrors `pkg/secretscan.Finding`
    but is decoupled so the wire schema can evolve
    independently of the scanner's internal fields. `snippet`
    is the pre-truncated safe representation (first 6 chars +
    "…" + last 4) — never the raw value, matching the snippet
    policy documented in `pkg/secretscan/scan.go`. Closed-set
    `provider` keys: stripe_live, stripe_test, github_pat,
    aws_access, openai, anthropic, private_key_block. The
    optional `layer` field (PR-A) names the per-walk source
    label — "app" for the main image, "sidecar-<slug>" for
    each sidecar, or absent for the apid source-tree
    rejection path (legacy).

    """

    file: str
    line: int
    provider: str
    severity: SecretFindingSeverity
    snippet: str
    key: str | Unset = UNSET
    layer: str | Unset = UNSET
    """Per-walk source label (PR-A). "app" for findings in
    the main image; "sidecar-<slug>" for findings in a
    sidecar (e.g. "sidecar-redis"); absent or "" for
    findings from the legacy apid source-tree rejection
    path (cmd/apid/secretscan.go).
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        file = self.file

        line = self.line

        provider = self.provider

        severity: str = self.severity

        snippet = self.snippet

        key = self.key

        layer = self.layer

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "file": file,
                "line": line,
                "provider": provider,
                "severity": severity,
                "snippet": snippet,
            }
        )
        if key is not UNSET:
            field_dict["key"] = key
        if layer is not UNSET:
            field_dict["layer"] = layer

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        file = d.pop("file")

        line = d.pop("line")

        provider = d.pop("provider")

        severity = check_secret_finding_severity(d.pop("severity"))

        snippet = d.pop("snippet")

        key = d.pop("key", UNSET)

        layer = d.pop("layer", UNSET)

        secret_finding = cls(
            file=file,
            line=line,
            provider=provider,
            severity=severity,
            snippet=snippet,
            key=key,
            layer=layer,
        )

        secret_finding.additional_properties = d
        return secret_finding

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
