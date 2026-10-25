from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_rollback_deployment_evidence_status import (
    AlertRollbackDeploymentEvidenceStatus,
    check_alert_rollback_deployment_evidence_status,
)
from ..models.alert_rollback_deployment_evidence_version import (
    AlertRollbackDeploymentEvidenceVersion,
    check_alert_rollback_deployment_evidence_version,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AlertRollbackDeploymentEvidence")


@_attrs_define
class AlertRollbackDeploymentEvidence:
    """Exact deployment request evidence for post-deploy rollback. Windows contain only complete minutes after cutover and
    before the fire with 30 seconds of ingestion lag. Requests counts only 2xx and 5xx responses, weighted by telemetry
    publisher counts. At least 20 requests, one server error, and a sample within two minutes of the window end are
    required. Only error_rate_pct with gt or gte comparisons qualifies. Unaccepted fires expire after two minutes.
    Accepted evidence is immutable and included in intent and completion audits; existing accepted operations continue
    without requalification.

    """

    version: AlertRollbackDeploymentEvidenceVersion
    deployment_id: UUID
    metric: str
    comparison: str
    threshold: float
    window_spec: str
    cutover_at: datetime.datetime
    window_start: datetime.datetime
    window_end: datetime.datetime
    requests: int
    server_errors: int
    minimum_requests: int
    error_rate_pct: float
    status: AlertRollbackDeploymentEvidenceStatus
    checked_at: datetime.datetime | Unset = UNSET
    last_sample_at: datetime.datetime | Unset = UNSET
    code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        deployment_id = str(self.deployment_id)

        metric = self.metric

        comparison = self.comparison

        threshold = self.threshold

        window_spec = self.window_spec

        cutover_at = self.cutover_at.isoformat()

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        requests = self.requests

        server_errors = self.server_errors

        minimum_requests = self.minimum_requests

        error_rate_pct = self.error_rate_pct

        status: str = self.status

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        last_sample_at: str | Unset = UNSET
        if not isinstance(self.last_sample_at, Unset):
            last_sample_at = self.last_sample_at.isoformat()

        code = self.code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "deployment_id": deployment_id,
                "metric": metric,
                "comparison": comparison,
                "threshold": threshold,
                "window_spec": window_spec,
                "cutover_at": cutover_at,
                "window_start": window_start,
                "window_end": window_end,
                "requests": requests,
                "server_errors": server_errors,
                "minimum_requests": minimum_requests,
                "error_rate_pct": error_rate_pct,
                "status": status,
            }
        )
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if last_sample_at is not UNSET:
            field_dict["last_sample_at"] = last_sample_at
        if code is not UNSET:
            field_dict["code"] = code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_alert_rollback_deployment_evidence_version(d.pop("version"))

        deployment_id = UUID(d.pop("deployment_id"))

        metric = d.pop("metric")

        comparison = d.pop("comparison")

        threshold = d.pop("threshold")

        window_spec = d.pop("window_spec")

        cutover_at = datetime.datetime.fromisoformat(d.pop("cutover_at"))

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        requests = d.pop("requests")

        server_errors = d.pop("server_errors")

        minimum_requests = d.pop("minimum_requests")

        error_rate_pct = d.pop("error_rate_pct")

        status = check_alert_rollback_deployment_evidence_status(d.pop("status"))

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _last_sample_at = d.pop("last_sample_at", UNSET)
        last_sample_at: datetime.datetime | Unset
        if isinstance(_last_sample_at, Unset):
            last_sample_at = UNSET
        else:
            last_sample_at = datetime.datetime.fromisoformat(_last_sample_at)

        code = d.pop("code", UNSET)

        alert_rollback_deployment_evidence = cls(
            version=version,
            deployment_id=deployment_id,
            metric=metric,
            comparison=comparison,
            threshold=threshold,
            window_spec=window_spec,
            cutover_at=cutover_at,
            window_start=window_start,
            window_end=window_end,
            requests=requests,
            server_errors=server_errors,
            minimum_requests=minimum_requests,
            error_rate_pct=error_rate_pct,
            status=status,
            checked_at=checked_at,
            last_sample_at=last_sample_at,
            code=code,
        )

        alert_rollback_deployment_evidence.additional_properties = d
        return alert_rollback_deployment_evidence

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
