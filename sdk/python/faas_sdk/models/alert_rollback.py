from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_rollback_rollback_phase import AlertRollbackRollbackPhase, check_alert_rollback_rollback_phase
from ..models.alert_rollback_service_phase import AlertRollbackServicePhase, check_alert_rollback_service_phase
from ..models.alert_rollback_status import AlertRollbackStatus, check_alert_rollback_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.alert_rollback_deployment_evidence import AlertRollbackDeploymentEvidence
    from ..models.binding_check_finding import BindingCheckFinding


T = TypeVar("T", bound="AlertRollback")


@_attrs_define
class AlertRollback:
    """Durable exact canary, active service, or opt-in completed-release rollback captured atomically with one production
    alert fire. Historical actions link one checked rollback operation and complete only after readiness, checked
    routing and service handoff barriers. Service actions pin the retained predecessor and complete only after the
    matching abort handoff finishes. Binding evidence is never reusable. Unavailable or ambiguous selections fail
    closed. Deleting the alert rule deletes its delivery and action ledger.

    """

    id: UUID
    rule_id: UUID
    account_id: UUID
    app_id: UUID
    scope: str
    status: AlertRollbackStatus
    reason: str
    observed_value: float
    fired_at: datetime.datetime
    updated_at: datetime.datetime
    deployment_evidence: AlertRollbackDeploymentEvidence | Unset = UNSET
    """Exact deployment request evidence for post-deploy rollback. Windows contain only complete minutes after
    cutover and before the fire with 30 seconds of ingestion lag. Requests counts only 2xx and 5xx responses,
    weighted by telemetry publisher counts. At least 20 requests, one server error, and a sample within two minutes
    of the window end are required. Only error_rate_pct with gt or gte comparisons qualifies. Unaccepted fires
    expire after two minutes. Accepted evidence is immutable and included in intent and completion audits; existing
    accepted operations continue without requalification."""
    historical: bool | Unset = UNSET
    rollback_operation_id: UUID | Unset = UNSET
    rollback_phase: AlertRollbackRollbackPhase | Unset = UNSET
    rollback_routing_audit_id: str | Unset = UNSET
    candidate_deployment_id: UUID | Unset = UNSET
    predecessor_deployment_id: UUID | Unset = UNSET
    code: str | Unset = UNSET
    blockers: list[BindingCheckFinding] | Unset = UNSET
    completed_at: datetime.datetime | None | Unset = UNSET
    audit_id: str | Unset = UNSET
    service: bool | Unset = UNSET
    service_request_id: UUID | Unset = UNSET
    service_phase: AlertRollbackServicePhase | Unset = UNSET
    service_routing_audit_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        rule_id = str(self.rule_id)

        account_id = str(self.account_id)

        app_id = str(self.app_id)

        scope = self.scope

        status: str = self.status

        reason = self.reason

        observed_value = self.observed_value

        fired_at = self.fired_at.isoformat()

        updated_at = self.updated_at.isoformat()

        deployment_evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.deployment_evidence, Unset):
            deployment_evidence = self.deployment_evidence.to_dict()

        historical = self.historical

        rollback_operation_id: str | Unset = UNSET
        if not isinstance(self.rollback_operation_id, Unset):
            rollback_operation_id = str(self.rollback_operation_id)

        rollback_phase: str | Unset = UNSET
        if not isinstance(self.rollback_phase, Unset):
            rollback_phase = self.rollback_phase

        rollback_routing_audit_id = self.rollback_routing_audit_id

        candidate_deployment_id: str | Unset = UNSET
        if not isinstance(self.candidate_deployment_id, Unset):
            candidate_deployment_id = str(self.candidate_deployment_id)

        predecessor_deployment_id: str | Unset = UNSET
        if not isinstance(self.predecessor_deployment_id, Unset):
            predecessor_deployment_id = str(self.predecessor_deployment_id)

        code = self.code

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        completed_at: None | str | Unset
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        audit_id = self.audit_id

        service = self.service

        service_request_id: str | Unset = UNSET
        if not isinstance(self.service_request_id, Unset):
            service_request_id = str(self.service_request_id)

        service_phase: str | Unset = UNSET
        if not isinstance(self.service_phase, Unset):
            service_phase = self.service_phase

        service_routing_audit_id = self.service_routing_audit_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "rule_id": rule_id,
                "account_id": account_id,
                "app_id": app_id,
                "scope": scope,
                "status": status,
                "reason": reason,
                "observed_value": observed_value,
                "fired_at": fired_at,
                "updated_at": updated_at,
            }
        )
        if deployment_evidence is not UNSET:
            field_dict["deployment_evidence"] = deployment_evidence
        if historical is not UNSET:
            field_dict["historical"] = historical
        if rollback_operation_id is not UNSET:
            field_dict["rollback_operation_id"] = rollback_operation_id
        if rollback_phase is not UNSET:
            field_dict["rollback_phase"] = rollback_phase
        if rollback_routing_audit_id is not UNSET:
            field_dict["rollback_routing_audit_id"] = rollback_routing_audit_id
        if candidate_deployment_id is not UNSET:
            field_dict["candidate_deployment_id"] = candidate_deployment_id
        if predecessor_deployment_id is not UNSET:
            field_dict["predecessor_deployment_id"] = predecessor_deployment_id
        if code is not UNSET:
            field_dict["code"] = code
        if blockers is not UNSET:
            field_dict["blockers"] = blockers
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if audit_id is not UNSET:
            field_dict["audit_id"] = audit_id
        if service is not UNSET:
            field_dict["service"] = service
        if service_request_id is not UNSET:
            field_dict["service_request_id"] = service_request_id
        if service_phase is not UNSET:
            field_dict["service_phase"] = service_phase
        if service_routing_audit_id is not UNSET:
            field_dict["service_routing_audit_id"] = service_routing_audit_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.alert_rollback_deployment_evidence import AlertRollbackDeploymentEvidence
        from ..models.binding_check_finding import BindingCheckFinding

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        rule_id = UUID(d.pop("rule_id"))

        account_id = UUID(d.pop("account_id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        status = check_alert_rollback_status(d.pop("status"))

        reason = d.pop("reason")

        observed_value = d.pop("observed_value")

        fired_at = datetime.datetime.fromisoformat(d.pop("fired_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _deployment_evidence = d.pop("deployment_evidence", UNSET)
        deployment_evidence: AlertRollbackDeploymentEvidence | Unset
        if isinstance(_deployment_evidence, Unset):
            deployment_evidence = UNSET
        else:
            deployment_evidence = AlertRollbackDeploymentEvidence.from_dict(_deployment_evidence)

        historical = d.pop("historical", UNSET)

        _rollback_operation_id = d.pop("rollback_operation_id", UNSET)
        rollback_operation_id: UUID | Unset
        if isinstance(_rollback_operation_id, Unset):
            rollback_operation_id = UNSET
        else:
            rollback_operation_id = UUID(_rollback_operation_id)

        _rollback_phase = d.pop("rollback_phase", UNSET)
        rollback_phase: AlertRollbackRollbackPhase | Unset
        if isinstance(_rollback_phase, Unset):
            rollback_phase = UNSET
        else:
            rollback_phase = check_alert_rollback_rollback_phase(_rollback_phase)

        rollback_routing_audit_id = d.pop("rollback_routing_audit_id", UNSET)

        _candidate_deployment_id = d.pop("candidate_deployment_id", UNSET)
        candidate_deployment_id: UUID | Unset
        if isinstance(_candidate_deployment_id, Unset):
            candidate_deployment_id = UNSET
        else:
            candidate_deployment_id = UUID(_candidate_deployment_id)

        _predecessor_deployment_id = d.pop("predecessor_deployment_id", UNSET)
        predecessor_deployment_id: UUID | Unset
        if isinstance(_predecessor_deployment_id, Unset):
            predecessor_deployment_id = UNSET
        else:
            predecessor_deployment_id = UUID(_predecessor_deployment_id)

        code = d.pop("code", UNSET)

        _blockers = d.pop("blockers", UNSET)
        blockers: list[BindingCheckFinding] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = BindingCheckFinding.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        def _parse_completed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = datetime.datetime.fromisoformat(data)

                return completed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        audit_id = d.pop("audit_id", UNSET)

        service = d.pop("service", UNSET)

        _service_request_id = d.pop("service_request_id", UNSET)
        service_request_id: UUID | Unset
        if isinstance(_service_request_id, Unset):
            service_request_id = UNSET
        else:
            service_request_id = UUID(_service_request_id)

        _service_phase = d.pop("service_phase", UNSET)
        service_phase: AlertRollbackServicePhase | Unset
        if isinstance(_service_phase, Unset):
            service_phase = UNSET
        else:
            service_phase = check_alert_rollback_service_phase(_service_phase)

        service_routing_audit_id = d.pop("service_routing_audit_id", UNSET)

        alert_rollback = cls(
            id=id,
            rule_id=rule_id,
            account_id=account_id,
            app_id=app_id,
            scope=scope,
            status=status,
            reason=reason,
            observed_value=observed_value,
            fired_at=fired_at,
            updated_at=updated_at,
            deployment_evidence=deployment_evidence,
            historical=historical,
            rollback_operation_id=rollback_operation_id,
            rollback_phase=rollback_phase,
            rollback_routing_audit_id=rollback_routing_audit_id,
            candidate_deployment_id=candidate_deployment_id,
            predecessor_deployment_id=predecessor_deployment_id,
            code=code,
            blockers=blockers,
            completed_at=completed_at,
            audit_id=audit_id,
            service=service,
            service_request_id=service_request_id,
            service_phase=service_phase,
            service_routing_audit_id=service_routing_audit_id,
        )

        alert_rollback.additional_properties = d
        return alert_rollback

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
