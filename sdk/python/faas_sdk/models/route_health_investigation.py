from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_investigation_coverage import (
    RouteHealthInvestigationCoverage,
    check_route_health_investigation_coverage,
)
from ..models.route_health_investigation_evidence_status import (
    RouteHealthInvestigationEvidenceStatus,
    check_route_health_investigation_evidence_status,
)
from ..models.route_health_investigation_status import (
    RouteHealthInvestigationStatus,
    check_route_health_investigation_status,
)

if TYPE_CHECKING:
    from ..models.route_health_finding import RouteHealthFinding
    from ..models.route_health_investigation_selection import RouteHealthInvestigationSelection
    from ..models.route_health_investigation_window import RouteHealthInvestigationWindow
    from ..models.route_health_report import RouteHealthReport


T = TypeVar("T", bound="RouteHealthInvestigation")


@_attrs_define
class RouteHealthInvestigation:
    """Shareable read-only investigation. The full aggregate report preserves rollout health context; finding is the
    selected aggregate or customer-specific route. Status and reason describe the selected 5xx or watched-code signal,
    independently of rollout decisions. Examples reference retained metadata only. Missing stable comparisons return
    explicit unavailable evidence and zero rows. Coverage stays observed_only.

    """

    version: int
    selection: RouteHealthInvestigationSelection
    """Exact configured route and signal, optionally scoped to a recorded customer identity."""
    report: RouteHealthReport
    """Current observed-only critical-route health comparison with candidate, predecessor, policy, and telemetry
    provenance."""
    finding: RouteHealthFinding
    """Combined verdict and both closed-window evidence records for one selected critical route."""
    status: RouteHealthInvestigationStatus
    reason: str
    coverage: RouteHealthInvestigationCoverage
    evidence_status: RouteHealthInvestigationEvidenceStatus
    examples_limit: int
    windows: list[RouteHealthInvestigationWindow]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        selection = self.selection.to_dict()

        report = self.report.to_dict()

        finding = self.finding.to_dict()

        status: str = self.status

        reason = self.reason

        coverage: str = self.coverage

        evidence_status: str = self.evidence_status

        examples_limit = self.examples_limit

        windows = []
        for windows_item_data in self.windows:
            windows_item = windows_item_data.to_dict()
            windows.append(windows_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "selection": selection,
                "report": report,
                "finding": finding,
                "status": status,
                "reason": reason,
                "coverage": coverage,
                "evidence_status": evidence_status,
                "examples_limit": examples_limit,
                "windows": windows,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_finding import RouteHealthFinding
        from ..models.route_health_investigation_selection import RouteHealthInvestigationSelection
        from ..models.route_health_investigation_window import RouteHealthInvestigationWindow
        from ..models.route_health_report import RouteHealthReport

        d = dict(src_dict)
        version = d.pop("version")

        selection = RouteHealthInvestigationSelection.from_dict(d.pop("selection"))

        report = RouteHealthReport.from_dict(d.pop("report"))

        finding = RouteHealthFinding.from_dict(d.pop("finding"))

        status = check_route_health_investigation_status(d.pop("status"))

        reason = d.pop("reason")

        coverage = check_route_health_investigation_coverage(d.pop("coverage"))

        evidence_status = check_route_health_investigation_evidence_status(d.pop("evidence_status"))

        examples_limit = d.pop("examples_limit")

        windows = []
        _windows = d.pop("windows")
        for windows_item_data in _windows:
            windows_item = RouteHealthInvestigationWindow.from_dict(windows_item_data)

            windows.append(windows_item)

        route_health_investigation = cls(
            version=version,
            selection=selection,
            report=report,
            finding=finding,
            status=status,
            reason=reason,
            coverage=coverage,
            evidence_status=evidence_status,
            examples_limit=examples_limit,
            windows=windows,
        )

        route_health_investigation.additional_properties = d
        return route_health_investigation

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
