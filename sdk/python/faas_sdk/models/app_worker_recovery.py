from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_worker_recovery_scope import AppWorkerRecoveryScope, check_app_worker_recovery_scope
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_worker_pool_observation import AppWorkerPoolObservation


T = TypeVar("T", bound="AppWorkerRecovery")


@_attrs_define
class AppWorkerRecovery:
    """Read-only app-wide worker retry status and last scheduler pool observations. Environment selection filters pools;
    retry status remains app-wide. Missing evidence never establishes health.

    """

    available: bool
    eligible: bool
    """Whether the app and account permit automatic recovery."""
    scope: AppWorkerRecoveryScope
    status: str
    """Last app-wide recovery outcome; unknown and not_observed indicate missing evidence."""
    failures: int
    pools: list[AppWorkerPoolObservation]
    reason: str | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    lease_until: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        eligible = self.eligible

        scope: str = self.scope

        status = self.status

        failures = self.failures

        pools = []
        for pools_item_data in self.pools:
            pools_item = pools_item_data.to_dict()
            pools.append(pools_item)

        reason = self.reason

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        lease_until: str | Unset = UNSET
        if not isinstance(self.lease_until, Unset):
            lease_until = self.lease_until.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "available": available,
                "eligible": eligible,
                "scope": scope,
                "status": status,
                "failures": failures,
                "pools": pools,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if lease_until is not UNSET:
            field_dict["lease_until"] = lease_until
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_worker_pool_observation import AppWorkerPoolObservation

        d = dict(src_dict)
        available = d.pop("available")

        eligible = d.pop("eligible")

        scope = check_app_worker_recovery_scope(d.pop("scope"))

        status = d.pop("status")

        failures = d.pop("failures")

        pools = []
        _pools = d.pop("pools")
        for pools_item_data in _pools:
            pools_item = AppWorkerPoolObservation.from_dict(pools_item_data)

            pools.append(pools_item)

        reason = d.pop("reason", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _lease_until = d.pop("lease_until", UNSET)
        lease_until: datetime.datetime | Unset
        if isinstance(_lease_until, Unset):
            lease_until = UNSET
        else:
            lease_until = datetime.datetime.fromisoformat(_lease_until)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        app_worker_recovery = cls(
            available=available,
            eligible=eligible,
            scope=scope,
            status=status,
            failures=failures,
            pools=pools,
            reason=reason,
            next_attempt_at=next_attempt_at,
            lease_until=lease_until,
            updated_at=updated_at,
        )

        app_worker_recovery.additional_properties = d
        return app_worker_recovery

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
