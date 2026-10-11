"""Publish committed Customer Operation facts from the application outbox."""

from __future__ import annotations

import datetime as dt
import json
import re
from typing import Any
from uuid import UUID

from ._operation_contract import (
    OPERATION_MILESTONE_PAYLOAD_BYTES,
    OPERATION_MILESTONES,
    OPERATION_WORKFLOW_STATE_REPORTS,
)
from .customer_operations import CustomerOperationRequest, customer_operation_request_digest
from .models.operation_milestone import OperationMilestone
from .models.operation_milestone_request import OperationMilestoneRequest
from .models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone
from .models.operation_workflow_state_report import OperationWorkflowStateReport
from .models.operation_workflow_state_report_response import OperationWorkflowStateReportResponse
from .types import UNSET

_UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\Z")
_MILESTONE = re.compile(r"[a-z][a-z0-9-]{0,63}\Z")
_WORKFLOW = re.compile(r"[a-z][a-z0-9-]{0,62}\Z")
_STATE = re.compile(r"[a-z][a-z0-9-]{0,63}\Z")


def _tuple_row(_cursor: Any):
    return tuple


def _utc(value: dt.datetime) -> dt.datetime:
    if value.tzinfo is None:
        return value.replace(tzinfo=dt.timezone.utc)
    return value.astimezone(dt.timezone.utc)


def _reject_json_constant(value: str) -> None:
    raise ValueError(f"invalid JSON constant: {value}")


async def apublish_customer_operation_milestones(
    connection: Any,
    request: CustomerOperationRequest,
    publish,
) -> None:
    digest = customer_operation_request_digest(request)
    async with connection.cursor(row_factory=_tuple_row) as cursor:
        await cursor.execute(
            "SELECT m.id::text,m.name,m.payload,m.occurred_at "
            "FROM public.gregale_customer_operation_milestones m "
            "JOIN public.gregale_customer_operation_inbox r ON r.operation_id=m.operation_id "
            "WHERE r.operation_id=%s::uuid AND r.account_id=%s::uuid AND r.app_id=%s::uuid "
            "AND r.platform_tenant_id=%s::uuid AND r.request_digest=%s AND m.acknowledged_at IS NULL "
            "ORDER BY m.occurred_at,m.id LIMIT %s",
            (
                request.operation_id,
                request.account_id,
                request.app_id,
                request.platform_tenant_id,
                digest,
                OPERATION_MILESTONES + 1,
            ),
        )
        pending = await cursor.fetchall()
    if len(pending) > OPERATION_MILESTONES:
        raise ValueError("saved Customer Operation milestone count exceeds its bound")

    for row in pending:
        identity, name, payload, occurred_at = row
        if (
            not isinstance(identity, str)
            or not _UUID.fullmatch(identity)
            or not isinstance(name, str)
            or not _MILESTONE.fullmatch(name)
            or not isinstance(payload, str)
            or len(payload.encode("utf-8")) > OPERATION_MILESTONE_PAYLOAD_BYTES
        ):
            raise ValueError("invalid saved Customer Operation milestone")
        decoded = json.loads(payload, parse_constant=_reject_json_constant)
        if not isinstance(occurred_at, dt.datetime):
            raise ValueError("invalid saved Customer Operation milestone timestamp")
        report = OperationMilestoneRequest(
            id=UUID(identity), name=name, payload=decoded, occurred_at=_utc(occurred_at)
        )
        receipt: OperationMilestone = await publish(report)
        if str(receipt.id) != identity or str(receipt.operation_id) != request.operation_id or receipt.name != name:
            raise ValueError("Customer Operation milestone publication identity was not confirmed")

        async with connection.cursor() as cursor:
            await cursor.execute(
                "UPDATE public.gregale_customer_operation_milestones m SET acknowledged_at=clock_timestamp() "
                "FROM public.gregale_customer_operation_inbox r "
                "WHERE m.operation_id=r.operation_id AND r.operation_id=%s::uuid "
                "AND r.account_id=%s::uuid AND r.app_id=%s::uuid AND r.platform_tenant_id=%s::uuid "
                "AND r.request_digest=%s AND m.id=%s::uuid",
                (
                    request.operation_id,
                    request.account_id,
                    request.app_id,
                    request.platform_tenant_id,
                    digest,
                    identity,
                ),
            )


async def apublish_customer_operation_workflow_states(
    connection: Any,
    request: CustomerOperationRequest,
    publish,
) -> None:
    digest = customer_operation_request_digest(request)
    async with connection.cursor(row_factory=_tuple_row) as cursor:
        await cursor.execute(
            "SELECT s.id::text,s.workflow,s.instance_id,s.from_state,s.state,s.revision,"
            "s.evidence_milestones,s.occurred_at,s.blockers,s.blockers_only,s.blocker_resolutions,s.deadline_at,s.deadline_only,s.outcome_code,s.outcome_description,s.outcome_only,s.depends_on,s.dependencies_only "
            "FROM public.gregale_customer_operation_workflow_states s "
            "JOIN public.gregale_customer_operation_inbox r ON r.operation_id=s.operation_id "
            "WHERE r.operation_id=%s::uuid AND r.account_id=%s::uuid AND r.app_id=%s::uuid "
            "AND r.platform_tenant_id=%s::uuid AND r.request_digest=%s AND s.acknowledged_at IS NULL "
            "ORDER BY s.revision,s.id LIMIT %s",
            (
                request.operation_id,
                request.account_id,
                request.app_id,
                request.platform_tenant_id,
                digest,
                OPERATION_WORKFLOW_STATE_REPORTS + 1,
            ),
        )
        pending = await cursor.fetchall()
    if len(pending) > OPERATION_WORKFLOW_STATE_REPORTS:
        raise ValueError("saved workflow state count exceeds its bound")

    for row in pending:
        identity, workflow, instance_id, from_state, state, revision, evidence, occurred_at, blockers, blockers_only, resolutions, deadline_at, deadline_only, outcome_code, outcome_description, outcome_only, dependencies, dependencies_only = row
        if (
            not isinstance(identity, str)
            or not _UUID.fullmatch(identity)
            or not isinstance(workflow, str)
            or not _WORKFLOW.fullmatch(workflow)
            or not isinstance(instance_id, str)
            or not instance_id
            or len(instance_id.encode("utf-8")) > 256
            or any(ord(char) < 0x20 or ord(char) == 0x7F for char in instance_id)
            or not isinstance(state, str)
            or not _STATE.fullmatch(state)
            or (from_state and (not isinstance(from_state, str) or not _STATE.fullmatch(from_state)))
            or type(revision) is not int
            or not 1 <= revision <= 9_007_199_254_740_991
            or not isinstance(occurred_at, dt.datetime)
        ):
            raise ValueError("invalid saved Customer Operation workflow state")
        if isinstance(evidence, str):
            evidence = json.loads(evidence)
        if not isinstance(evidence, list) or len(evidence) > 16:
            raise ValueError("invalid saved workflow state evidence")
        evidence_models: list[OperationWorkflowEvidenceMilestone] = []
        for item in evidence:
            if (
                not isinstance(item, dict)
                or not isinstance(item.get("id"), str)
                or not _UUID.fullmatch(item["id"])
                or not isinstance(item.get("name"), str)
                or not _MILESTONE.fullmatch(item["name"])
            ):
                raise ValueError("invalid saved workflow state evidence")
            evidence_models.append(OperationWorkflowEvidenceMilestone(id=UUID(item["id"]), name=item["name"]))

        report_data: dict[str, Any] = {
            "id": identity,
            "workflow": workflow,
            "instance_id": instance_id,
            "state": state,
            "revision": revision,
            "occurred_at": _utc(occurred_at).isoformat(),
        }
        if from_state:
            report_data["from_state"] = from_state
        if evidence_models:
            report_data["evidence_milestones"] = [item.to_dict() for item in evidence_models]
        from .customer_operations import _canonical_workflow_blockers, _canonical_workflow_resolutions, _canonical_workflow_deadline, _validate_workflow_outcome, _canonical_workflow_dependencies
        from .models.operation_workflow_blocker_resolution import OperationWorkflowBlockerResolution
        from .models.operation_workflow_blocker import OperationWorkflowBlocker
        if isinstance(blockers, str):
            blockers = json.loads(blockers)
        if not isinstance(blockers, list):
            raise ValueError("invalid saved workflow blockers")
        blocker_models = _canonical_workflow_blockers([OperationWorkflowBlocker.from_dict(b) for b in blockers])
        report_data["blockers"] = [b.to_dict() for b in blocker_models]
        if isinstance(resolutions, str):
            resolutions = json.loads(resolutions)
        if not isinstance(resolutions, list):
            raise ValueError("invalid saved workflow resolutions")
        resolution_models = _canonical_workflow_resolutions([OperationWorkflowBlockerResolution.from_dict(v) for v in resolutions], blocker_models)
        report_data["blocker_resolutions"] = [v.to_dict() for v in resolution_models]
        from .models.operation_workflow_dependency import OperationWorkflowDependency
        if isinstance(dependencies,str): dependencies=json.loads(dependencies)
        dependency_models=_canonical_workflow_dependencies([OperationWorkflowDependency.from_dict(d) for d in dependencies])
        report_data["depends_on"]=[d.to_dict() for d in dependency_models]
        if dependencies_only: report_data["dependencies_only"]=True
        if outcome_code or outcome_description:
            _validate_workflow_outcome(outcome_code, outcome_description)
            report_data["outcome_code"], report_data["outcome_description"] = outcome_code, outcome_description
        if outcome_only: report_data["outcome_only"] = True
        if deadline_at: report_data["deadline_at"] = _canonical_workflow_deadline(deadline_at)
        if deadline_only: report_data["deadline_only"] = True
        if blockers_only:
            report_data["blockers_only"] = True
        report = OperationWorkflowStateReport.from_dict(report_data)
        receipt: OperationWorkflowStateReportResponse = await publish(report)
        report_from = report.from_state if report.from_state is not UNSET else UNSET
        receipt_from = receipt.from_state if receipt.from_state is not UNSET else UNSET
        report_evidence = [] if report.evidence_milestones is UNSET else report.evidence_milestones
        receipt_evidence = [] if receipt.evidence_milestones is UNSET else receipt.evidence_milestones
        receipt_blockers = _canonical_workflow_blockers([] if receipt.blockers is UNSET else receipt.blockers)
        receipt_resolutions = _canonical_workflow_resolutions([] if receipt.blocker_resolutions is UNSET else receipt.blocker_resolutions, receipt_blockers)
        if (
            [v.to_dict() for v in receipt_resolutions] != [v.to_dict() for v in resolution_models]
            or [d.to_dict() for d in _canonical_workflow_dependencies([] if receipt.depends_on is UNSET else receipt.depends_on)] != [d.to_dict() for d in dependency_models]
            or (receipt.dependencies_only is True)!=(report.dependencies_only is True)
            or ("" if receipt.outcome_code is UNSET else receipt.outcome_code) != ("" if report.outcome_code is UNSET else report.outcome_code)
            or ("" if receipt.outcome_description is UNSET else receipt.outcome_description) != ("" if report.outcome_description is UNSET else report.outcome_description)
            or (receipt.outcome_only is True) != (report.outcome_only is True)
            or _canonical_workflow_deadline("" if receipt.deadline_at is UNSET else receipt.deadline_at) != _canonical_workflow_deadline("" if report.deadline_at is UNSET else report.deadline_at)
            or (receipt.deadline_only is True) != (report.deadline_only is True)
            or (receipt.blockers_only is True) != (report.blockers_only is True)
            or [b.to_dict() for b in receipt_blockers] != [b.to_dict() for b in blocker_models]
            or str(receipt.id) != identity
            or str(receipt.operation_id) != request.operation_id
            or receipt.workflow != workflow
            or receipt.instance_id != instance_id
            or receipt.state != state
            or receipt.revision != revision
            or receipt.contract_version < 1
            or report_from != receipt_from
            or [(str(item.id), item.name) for item in receipt_evidence]
            != [(str(item.id), item.name) for item in report_evidence]
        ):
            raise ValueError("Customer Operation workflow state publication identity was not confirmed")

        async with connection.cursor() as cursor:
            await cursor.execute(
                "UPDATE public.gregale_customer_operation_workflow_states s SET acknowledged_at=clock_timestamp() "
                "FROM public.gregale_customer_operation_inbox r "
                "WHERE s.operation_id=r.operation_id AND r.operation_id=%s::uuid "
                "AND r.account_id=%s::uuid AND r.app_id=%s::uuid AND r.platform_tenant_id=%s::uuid "
                "AND r.request_digest=%s AND s.id=%s::uuid",
                (
                    request.operation_id,
                    request.account_id,
                    request.app_id,
                    request.platform_tenant_id,
                    digest,
                    identity,
                ),
            )
