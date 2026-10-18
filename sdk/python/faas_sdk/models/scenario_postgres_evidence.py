from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_postgres_evidence_provenance import (
    ScenarioPostgresEvidenceProvenance,
    check_scenario_postgres_evidence_provenance,
)
from ..models.scenario_postgres_evidence_status import (
    ScenarioPostgresEvidenceStatus,
    check_scenario_postgres_evidence_status,
)
from ..models.scenario_postgres_receipt_version import (
    ScenarioPostgresReceiptVersion,
    check_scenario_postgres_receipt_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scenario_postgres_delta import ScenarioPostgresDelta
    from ..models.scenario_postgres_snapshot import ScenarioPostgresSnapshot
    from ..models.scenario_postgres_spec import ScenarioPostgresSpec


T = TypeVar("T", bound="ScenarioPostgresEvidence")


@_attrs_define
class ScenarioPostgresEvidence:
    """Independent fixture observations with a fenced final capture and confirmed database/login deletion."""

    version: ScenarioPostgresReceiptVersion
    run_id: str
    app_id: UUID
    resource_id: UUID
    fixture_sha256: str
    contract_sha256: str
    initial_sha256: str
    status: ScenarioPostgresEvidenceStatus
    provenance: ScenarioPostgresEvidenceProvenance
    deployment_id: UUID
    spec: ScenarioPostgresSpec
    """Versioned fixture digest, selected table contract and sampled lock-wait assertion."""
    before: list[ScenarioPostgresSnapshot]
    after: list[ScenarioPostgresSnapshot]
    changes: list[ScenarioPostgresDelta]
    lock_samples: int
    max_observed_lock_wait_ms: int
    lock_budget_passed: bool
    cleanup_confirmed: bool
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        run_id = self.run_id

        app_id = str(self.app_id)

        resource_id = str(self.resource_id)

        fixture_sha256 = self.fixture_sha256

        contract_sha256 = self.contract_sha256

        initial_sha256 = self.initial_sha256

        status: str = self.status

        provenance: str = self.provenance

        deployment_id = str(self.deployment_id)

        spec = self.spec.to_dict()

        before = []
        for before_item_data in self.before:
            before_item = before_item_data.to_dict()
            before.append(before_item)

        after = []
        for after_item_data in self.after:
            after_item = after_item_data.to_dict()
            after.append(after_item)

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        lock_samples = self.lock_samples

        max_observed_lock_wait_ms = self.max_observed_lock_wait_ms

        lock_budget_passed = self.lock_budget_passed

        cleanup_confirmed = self.cleanup_confirmed

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "run_id": run_id,
                "app_id": app_id,
                "resource_id": resource_id,
                "fixture_sha256": fixture_sha256,
                "contract_sha256": contract_sha256,
                "initial_sha256": initial_sha256,
                "status": status,
                "provenance": provenance,
                "deployment_id": deployment_id,
                "spec": spec,
                "before": before,
                "after": after,
                "changes": changes,
                "lock_samples": lock_samples,
                "max_observed_lock_wait_ms": max_observed_lock_wait_ms,
                "lock_budget_passed": lock_budget_passed,
                "cleanup_confirmed": cleanup_confirmed,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_postgres_delta import ScenarioPostgresDelta
        from ..models.scenario_postgres_snapshot import ScenarioPostgresSnapshot
        from ..models.scenario_postgres_spec import ScenarioPostgresSpec

        d = dict(src_dict)
        version = check_scenario_postgres_receipt_version(d.pop("version"))

        run_id = d.pop("run_id")

        app_id = UUID(d.pop("app_id"))

        resource_id = UUID(d.pop("resource_id"))

        fixture_sha256 = d.pop("fixture_sha256")

        contract_sha256 = d.pop("contract_sha256")

        initial_sha256 = d.pop("initial_sha256")

        status = check_scenario_postgres_evidence_status(d.pop("status"))

        provenance = check_scenario_postgres_evidence_provenance(d.pop("provenance"))

        deployment_id = UUID(d.pop("deployment_id"))

        spec = ScenarioPostgresSpec.from_dict(d.pop("spec"))

        before = []
        _before = d.pop("before")
        for before_item_data in _before:
            before_item = ScenarioPostgresSnapshot.from_dict(before_item_data)

            before.append(before_item)

        after = []
        _after = d.pop("after")
        for after_item_data in _after:
            after_item = ScenarioPostgresSnapshot.from_dict(after_item_data)

            after.append(after_item)

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = ScenarioPostgresDelta.from_dict(changes_item_data)

            changes.append(changes_item)

        lock_samples = d.pop("lock_samples")

        max_observed_lock_wait_ms = d.pop("max_observed_lock_wait_ms")

        lock_budget_passed = d.pop("lock_budget_passed")

        cleanup_confirmed = d.pop("cleanup_confirmed")

        reason = d.pop("reason", UNSET)

        scenario_postgres_evidence = cls(
            version=version,
            run_id=run_id,
            app_id=app_id,
            resource_id=resource_id,
            fixture_sha256=fixture_sha256,
            contract_sha256=contract_sha256,
            initial_sha256=initial_sha256,
            status=status,
            provenance=provenance,
            deployment_id=deployment_id,
            spec=spec,
            before=before,
            after=after,
            changes=changes,
            lock_samples=lock_samples,
            max_observed_lock_wait_ms=max_observed_lock_wait_ms,
            lock_budget_passed=lock_budget_passed,
            cleanup_confirmed=cleanup_confirmed,
            reason=reason,
        )

        scenario_postgres_evidence.additional_properties = d
        return scenario_postgres_evidence

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
