from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_alert_collection_health import ObsAlertCollectionHealth
    from ..models.obs_alert_retention_health import ObsAlertRetentionHealth
    from ..models.obs_health_response_operator_intent_outcome_missing_total import (
        ObsHealthResponseOperatorIntentOutcomeMissingTotal,
    )
    from ..models.obs_health_response_signals import ObsHealthResponseSignals
    from ..models.obs_health_response_trace_id_completeness_ratio import ObsHealthResponseTraceIdCompletenessRatio


T = TypeVar("T", bound="ObsHealthResponse")


@_attrs_define
class ObsHealthResponse:
    """Wire shape for GET /v1/admin/obs/health (admin scope +
    FAAS_ADMIN_EMAILS allowlist + MFA). Composed from
    the API's own Prometheus counters (apid_audit_log_write_total /
    apid_audit_log_write_failures_total), a SQL audit-log coverage ratio,
    a single SQL aggregate over operator_intents (stuck-running
    rows), a single SQL aggregate over events (trace_id
    coverage), and a PromQL count of firing alerts. Kinds
    with zero rows in the SQL-derived fields are seeded to
    0 (counts) or 1.0 (ratios, vacuous truth) so the JSON
    shape stays stable on a fresh deploy.

    """

    generated_at: datetime.datetime
    """Response generation timestamp (UTC, RFC 3339); not a telemetry observation time."""
    prometheus_available: bool
    """True only when all three Prometheus signals are healthy with
    fresh underlying observations. When false, consult each signal:
    healthy values remain usable and other numeric values are zero
    compatibility fallbacks. On legacy responses without signals,
    false makes all Prometheus-derived values unavailable.
    """
    audit_log_write_total_5m: int
    """Increase of apid_audit_log_write_total over the trailing 5m
    window. Zero activity is a healthy zero only when the matching
    signals entry is healthy; otherwise zero is a compatibility fallback.
    """
    audit_log_write_failures_5m: int
    """Increase of apid_audit_log_write_failures_total over the trailing
    5m window. Interpret only when the matching signals entry is
    healthy, or prometheus_available is true on a legacy response.
    """
    audit_log_coverage_ratio_5m: float
    """Ratio of durable audit_log rows with a valid W3C trace_id
    in their JSON payload over all audit rows in the window. 1.0
    (vacuous truth) when no durable audit rows exist in the window.
    This ratio comes from SQL, independently of Prometheus availability.
    """
    operator_intent_outcome_missing_total: ObsHealthResponseOperatorIntentOutcomeMissingTotal
    """Per-kind count of operator_intents rows stuck in
    `running` past the 5m threshold. The handler seeds
    every kind in the operator-action vocabulary
    (force_park, force_cold_boot, force_restart) with
    0 so the JSON shape stays stable.
    """
    trace_id_completeness_ratio: ObsHealthResponseTraceIdCompletenessRatio
    """Per-kind ratio of operator.action.* events with a
    non-NULL trace_id over all operator.action.* events
    in the window. Kinds with zero rows are seeded to
    1.0 (vacuous truth — see Store interface comment).
    Reads events (live), NOT audit_log (FK-free
    post-deletion copy).
    """
    alerts_firing: int
    """Count of Prometheus alert rules in the firing state
    via PromQL ALERTS{alertstate="firing"}, excluding watchdog and
    info-only rules. A fresh faas.rules.yml evaluation heartbeat is
    required for the matching signal to be healthy, including when
    no alerts fire. This describes source freshness, not alert severity.
    """
    alert_history: ObsAlertCollectionHealth | Unset = UNSET
    """Durable collection receipt and explicit producer configuration or health state."""
    alert_retention: ObsAlertRetentionHealth | Unset = UNSET
    """Independent retention receipt and exact expired-episode backlog at the snapshot cutoff."""
    signals: ObsHealthResponseSignals | Unset = UNSET
    """Independent evidence for audit_log_write_total_5m,
    audit_log_write_failures_5m, and alerts_firing. Present on new
    servers; optional for compatibility with older responses.
    Only healthy signal values are usable. SQL-derived fields remain
    independent; a database read failure returns 503.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generated_at = self.generated_at.isoformat()

        prometheus_available = self.prometheus_available

        audit_log_write_total_5m = self.audit_log_write_total_5m

        audit_log_write_failures_5m = self.audit_log_write_failures_5m

        audit_log_coverage_ratio_5m = self.audit_log_coverage_ratio_5m

        operator_intent_outcome_missing_total = self.operator_intent_outcome_missing_total.to_dict()

        trace_id_completeness_ratio = self.trace_id_completeness_ratio.to_dict()

        alerts_firing = self.alerts_firing

        alert_history: dict[str, Any] | Unset = UNSET
        if not isinstance(self.alert_history, Unset):
            alert_history = self.alert_history.to_dict()

        alert_retention: dict[str, Any] | Unset = UNSET
        if not isinstance(self.alert_retention, Unset):
            alert_retention = self.alert_retention.to_dict()

        signals: dict[str, Any] | Unset = UNSET
        if not isinstance(self.signals, Unset):
            signals = self.signals.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generated_at": generated_at,
                "prometheus_available": prometheus_available,
                "audit_log_write_total_5m": audit_log_write_total_5m,
                "audit_log_write_failures_5m": audit_log_write_failures_5m,
                "audit_log_coverage_ratio_5m": audit_log_coverage_ratio_5m,
                "operator_intent_outcome_missing_total": operator_intent_outcome_missing_total,
                "trace_id_completeness_ratio": trace_id_completeness_ratio,
                "alerts_firing": alerts_firing,
            }
        )
        if alert_history is not UNSET:
            field_dict["alert_history"] = alert_history
        if alert_retention is not UNSET:
            field_dict["alert_retention"] = alert_retention
        if signals is not UNSET:
            field_dict["signals"] = signals

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_collection_health import ObsAlertCollectionHealth
        from ..models.obs_alert_retention_health import ObsAlertRetentionHealth
        from ..models.obs_health_response_operator_intent_outcome_missing_total import (
            ObsHealthResponseOperatorIntentOutcomeMissingTotal,
        )
        from ..models.obs_health_response_signals import ObsHealthResponseSignals
        from ..models.obs_health_response_trace_id_completeness_ratio import ObsHealthResponseTraceIdCompletenessRatio

        d = dict(src_dict)
        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        prometheus_available = d.pop("prometheus_available")

        audit_log_write_total_5m = d.pop("audit_log_write_total_5m")

        audit_log_write_failures_5m = d.pop("audit_log_write_failures_5m")

        audit_log_coverage_ratio_5m = d.pop("audit_log_coverage_ratio_5m")

        operator_intent_outcome_missing_total = ObsHealthResponseOperatorIntentOutcomeMissingTotal.from_dict(
            d.pop("operator_intent_outcome_missing_total")
        )

        trace_id_completeness_ratio = ObsHealthResponseTraceIdCompletenessRatio.from_dict(
            d.pop("trace_id_completeness_ratio")
        )

        alerts_firing = d.pop("alerts_firing")

        _alert_history = d.pop("alert_history", UNSET)
        alert_history: ObsAlertCollectionHealth | Unset
        if isinstance(_alert_history, Unset):
            alert_history = UNSET
        else:
            alert_history = ObsAlertCollectionHealth.from_dict(_alert_history)

        _alert_retention = d.pop("alert_retention", UNSET)
        alert_retention: ObsAlertRetentionHealth | Unset
        if isinstance(_alert_retention, Unset):
            alert_retention = UNSET
        else:
            alert_retention = ObsAlertRetentionHealth.from_dict(_alert_retention)

        _signals = d.pop("signals", UNSET)
        signals: ObsHealthResponseSignals | Unset
        if isinstance(_signals, Unset):
            signals = UNSET
        else:
            signals = ObsHealthResponseSignals.from_dict(_signals)

        obs_health_response = cls(
            generated_at=generated_at,
            prometheus_available=prometheus_available,
            audit_log_write_total_5m=audit_log_write_total_5m,
            audit_log_write_failures_5m=audit_log_write_failures_5m,
            audit_log_coverage_ratio_5m=audit_log_coverage_ratio_5m,
            operator_intent_outcome_missing_total=operator_intent_outcome_missing_total,
            trace_id_completeness_ratio=trace_id_completeness_ratio,
            alerts_firing=alerts_firing,
            alert_history=alert_history,
            alert_retention=alert_retention,
            signals=signals,
        )

        obs_health_response.additional_properties = d
        return obs_health_response

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
