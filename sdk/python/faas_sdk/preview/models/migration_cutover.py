from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.migration_cutover_status import MigrationCutoverStatus, check_migration_cutover_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_migration_cutover_request import CreateMigrationCutoverRequest
    from ..models.dns_record_instruction import DNSRecordInstruction
    from ..models.migration_cutover_traffic import MigrationCutoverTraffic
    from ..models.migration_verification_check import MigrationVerificationCheck


T = TypeVar("T", bound="MigrationCutover")


@_attrs_define
class MigrationCutover:
    """Saved plan, immutable recovery routing and bounded background cutover evidence. Ready means current DNS, trusted TLS
    and an exact-deployment gateway response passed; completion remains explicit.

    """

    id: UUID
    app: str
    deployment_id: UUID
    configuration_revision: str
    smoke_path: str
    status: MigrationCutoverStatus
    request: CreateMigrationCutoverRequest
    """Immutable production cutover plan. The stable ID reconciles lost responses. Preparation changes no traffic
    or domain bindings."""
    domain_connected: bool
    dns_records: list[DNSRecordInstruction]
    checks: list[MigrationVerificationCheck]
    traffic: MigrationCutoverTraffic
    """Existing deployment and exact GET route observations after hostname proof. Aggregates are not hostname-
    specific and never prove every customer moved."""
    created_at: datetime.datetime
    limited_traffic_acknowledged: bool
    reason: str | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    check_deadline: datetime.datetime | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    hostname_verified_at: datetime.datetime | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app = self.app

        deployment_id = str(self.deployment_id)

        configuration_revision = self.configuration_revision

        smoke_path = self.smoke_path

        status: str = self.status

        request = self.request.to_dict()

        domain_connected = self.domain_connected

        dns_records = []
        for dns_records_item_data in self.dns_records:
            dns_records_item = dns_records_item_data.to_dict()
            dns_records.append(dns_records_item)

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        traffic = self.traffic.to_dict()

        created_at = self.created_at.isoformat()

        limited_traffic_acknowledged = self.limited_traffic_acknowledged

        reason = self.reason

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        check_deadline: str | Unset = UNSET
        if not isinstance(self.check_deadline, Unset):
            check_deadline = self.check_deadline.isoformat()

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        hostname_verified_at: str | Unset = UNSET
        if not isinstance(self.hostname_verified_at, Unset):
            hostname_verified_at = self.hostname_verified_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app": app,
                "deployment_id": deployment_id,
                "configuration_revision": configuration_revision,
                "smoke_path": smoke_path,
                "status": status,
                "request": request,
                "domain_connected": domain_connected,
                "dns_records": dns_records,
                "checks": checks,
                "traffic": traffic,
                "created_at": created_at,
                "limited_traffic_acknowledged": limited_traffic_acknowledged,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if check_deadline is not UNSET:
            field_dict["check_deadline"] = check_deadline
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if hostname_verified_at is not UNSET:
            field_dict["hostname_verified_at"] = hostname_verified_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_migration_cutover_request import CreateMigrationCutoverRequest
        from ..models.dns_record_instruction import DNSRecordInstruction
        from ..models.migration_cutover_traffic import MigrationCutoverTraffic
        from ..models.migration_verification_check import MigrationVerificationCheck

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app = d.pop("app")

        deployment_id = UUID(d.pop("deployment_id"))

        configuration_revision = d.pop("configuration_revision")

        smoke_path = d.pop("smoke_path")

        status = check_migration_cutover_status(d.pop("status"))

        request = CreateMigrationCutoverRequest.from_dict(d.pop("request"))

        domain_connected = d.pop("domain_connected")

        dns_records = []
        _dns_records = d.pop("dns_records")
        for dns_records_item_data in _dns_records:
            dns_records_item = DNSRecordInstruction.from_dict(dns_records_item_data)

            dns_records.append(dns_records_item)

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = MigrationVerificationCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        traffic = MigrationCutoverTraffic.from_dict(d.pop("traffic"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        limited_traffic_acknowledged = d.pop("limited_traffic_acknowledged")

        reason = d.pop("reason", UNSET)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _check_deadline = d.pop("check_deadline", UNSET)
        check_deadline: datetime.datetime | Unset
        if isinstance(_check_deadline, Unset):
            check_deadline = UNSET
        else:
            check_deadline = datetime.datetime.fromisoformat(_check_deadline)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _hostname_verified_at = d.pop("hostname_verified_at", UNSET)
        hostname_verified_at: datetime.datetime | Unset
        if isinstance(_hostname_verified_at, Unset):
            hostname_verified_at = UNSET
        else:
            hostname_verified_at = datetime.datetime.fromisoformat(_hostname_verified_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        migration_cutover = cls(
            id=id,
            app=app,
            deployment_id=deployment_id,
            configuration_revision=configuration_revision,
            smoke_path=smoke_path,
            status=status,
            request=request,
            domain_connected=domain_connected,
            dns_records=dns_records,
            checks=checks,
            traffic=traffic,
            created_at=created_at,
            limited_traffic_acknowledged=limited_traffic_acknowledged,
            reason=reason,
            started_at=started_at,
            check_deadline=check_deadline,
            checked_at=checked_at,
            hostname_verified_at=hostname_verified_at,
            completed_at=completed_at,
        )

        migration_cutover.additional_properties = d
        return migration_cutover

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
