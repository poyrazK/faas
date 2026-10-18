from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_postgres_fault_receipt_state import (
    ScenarioPostgresFaultReceiptState,
    check_scenario_postgres_fault_receipt_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scenario_postgres_fault_spec import ScenarioPostgresFaultSpec


T = TypeVar("T", bound="ScenarioPostgresFaultReceipt")


@_attrs_define
class ScenarioPostgresFaultReceipt:
    """Durable original-owner confirmation of a database-specific connection fence and independent recovery probe. Missing
    proof remains inconclusive; a proven recovery exceeding the budget fails comparison.

    """

    id: UUID
    account_id: UUID
    run_id: str
    app_id: UUID
    resource_id: UUID
    deployment_id: UUID
    instance_id: UUID
    recorder_session_id: UUID
    invocation_id: UUID
    checkpoint_attempt_id: UUID
    owner_id: UUID
    backend_sha256: str
    plan_sha256: str
    spec: ScenarioPostgresFaultSpec
    """Separately gated warm rehearsal with one managed worker and one isolated database. Disconnects existing
    sessions and rejects new connections before restoring access. Excludes cold start, suspend/resume and worker
    termination."""
    state: ScenarioPostgresFaultReceiptState
    activated: bool
    recovered: bool
    closed_connections: int
    outage_ms: int
    """Confirmed active interval from disconnect acknowledgement until restoration starts."""
    recovery_ms: int
    """Elapsed time from planned restoration through a fresh connection and privilege probe."""
    error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = str(self.account_id)

        run_id = self.run_id

        app_id = str(self.app_id)

        resource_id = str(self.resource_id)

        deployment_id = str(self.deployment_id)

        instance_id = str(self.instance_id)

        recorder_session_id = str(self.recorder_session_id)

        invocation_id = str(self.invocation_id)

        checkpoint_attempt_id = str(self.checkpoint_attempt_id)

        owner_id = str(self.owner_id)

        backend_sha256 = self.backend_sha256

        plan_sha256 = self.plan_sha256

        spec = self.spec.to_dict()

        state: str = self.state

        activated = self.activated

        recovered = self.recovered

        closed_connections = self.closed_connections

        outage_ms = self.outage_ms

        recovery_ms = self.recovery_ms

        error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "run_id": run_id,
                "app_id": app_id,
                "resource_id": resource_id,
                "deployment_id": deployment_id,
                "instance_id": instance_id,
                "recorder_session_id": recorder_session_id,
                "invocation_id": invocation_id,
                "checkpoint_attempt_id": checkpoint_attempt_id,
                "owner_id": owner_id,
                "backend_sha256": backend_sha256,
                "plan_sha256": plan_sha256,
                "spec": spec,
                "state": state,
                "activated": activated,
                "recovered": recovered,
                "closed_connections": closed_connections,
                "outage_ms": outage_ms,
                "recovery_ms": recovery_ms,
            }
        )
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_postgres_fault_spec import ScenarioPostgresFaultSpec

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        account_id = UUID(d.pop("account_id"))

        run_id = d.pop("run_id")

        app_id = UUID(d.pop("app_id"))

        resource_id = UUID(d.pop("resource_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        instance_id = UUID(d.pop("instance_id"))

        recorder_session_id = UUID(d.pop("recorder_session_id"))

        invocation_id = UUID(d.pop("invocation_id"))

        checkpoint_attempt_id = UUID(d.pop("checkpoint_attempt_id"))

        owner_id = UUID(d.pop("owner_id"))

        backend_sha256 = d.pop("backend_sha256")

        plan_sha256 = d.pop("plan_sha256")

        spec = ScenarioPostgresFaultSpec.from_dict(d.pop("spec"))

        state = check_scenario_postgres_fault_receipt_state(d.pop("state"))

        activated = d.pop("activated")

        recovered = d.pop("recovered")

        closed_connections = d.pop("closed_connections")

        outage_ms = d.pop("outage_ms")

        recovery_ms = d.pop("recovery_ms")

        error = d.pop("error", UNSET)

        scenario_postgres_fault_receipt = cls(
            id=id,
            account_id=account_id,
            run_id=run_id,
            app_id=app_id,
            resource_id=resource_id,
            deployment_id=deployment_id,
            instance_id=instance_id,
            recorder_session_id=recorder_session_id,
            invocation_id=invocation_id,
            checkpoint_attempt_id=checkpoint_attempt_id,
            owner_id=owner_id,
            backend_sha256=backend_sha256,
            plan_sha256=plan_sha256,
            spec=spec,
            state=state,
            activated=activated,
            recovered=recovered,
            closed_connections=closed_connections,
            outage_ms=outage_ms,
            recovery_ms=recovery_ms,
            error=error,
        )

        scenario_postgres_fault_receipt.additional_properties = d
        return scenario_postgres_fault_receipt

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
