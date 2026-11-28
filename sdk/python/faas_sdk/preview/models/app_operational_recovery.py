from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_operational_rollback import AppOperationalRollback
    from ..models.app_worker_recovery import AppWorkerRecovery
    from ..models.runtime_config_restart_status_response import RuntimeConfigRestartStatusResponse


T = TypeVar("T", bound="AppOperationalRecovery")


@_attrs_define
class AppOperationalRecovery:
    """Bounded app-scoped pending rollbacks and pending or failed restart handoffs. Availability and truncation are
    explicit; accepted work does not establish completed recovery.

    """

    rollbacks_available: bool
    restarts_available: bool
    rollbacks_truncated: bool
    restarts_truncated: bool
    rollbacks: list[AppOperationalRollback]
    restarts: list[RuntimeConfigRestartStatusResponse]
    workers: AppWorkerRecovery | Unset = UNSET
    """Read-only app-wide worker retry status and last scheduler pool observations. Environment selection filters
    pools; retry status remains app-wide. Missing evidence never establishes health."""
    coverage: str | Unset = UNSET
    """Explicit scoped summaries include only attributable operations; app-wide restarts are excluded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rollbacks_available = self.rollbacks_available

        restarts_available = self.restarts_available

        rollbacks_truncated = self.rollbacks_truncated

        restarts_truncated = self.restarts_truncated

        rollbacks = []
        for rollbacks_item_data in self.rollbacks:
            rollbacks_item = rollbacks_item_data.to_dict()
            rollbacks.append(rollbacks_item)

        restarts = []
        for restarts_item_data in self.restarts:
            restarts_item = restarts_item_data.to_dict()
            restarts.append(restarts_item)

        workers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.workers, Unset):
            workers = self.workers.to_dict()

        coverage = self.coverage

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rollbacks_available": rollbacks_available,
                "restarts_available": restarts_available,
                "rollbacks_truncated": rollbacks_truncated,
                "restarts_truncated": restarts_truncated,
                "rollbacks": rollbacks,
                "restarts": restarts,
            }
        )
        if workers is not UNSET:
            field_dict["workers"] = workers
        if coverage is not UNSET:
            field_dict["coverage"] = coverage

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_operational_rollback import AppOperationalRollback
        from ..models.app_worker_recovery import AppWorkerRecovery
        from ..models.runtime_config_restart_status_response import RuntimeConfigRestartStatusResponse

        d = dict(src_dict)
        rollbacks_available = d.pop("rollbacks_available")

        restarts_available = d.pop("restarts_available")

        rollbacks_truncated = d.pop("rollbacks_truncated")

        restarts_truncated = d.pop("restarts_truncated")

        rollbacks = []
        _rollbacks = d.pop("rollbacks")
        for rollbacks_item_data in _rollbacks:
            rollbacks_item = AppOperationalRollback.from_dict(rollbacks_item_data)

            rollbacks.append(rollbacks_item)

        restarts = []
        _restarts = d.pop("restarts")
        for restarts_item_data in _restarts:
            restarts_item = RuntimeConfigRestartStatusResponse.from_dict(restarts_item_data)

            restarts.append(restarts_item)

        _workers = d.pop("workers", UNSET)
        workers: AppWorkerRecovery | Unset
        if isinstance(_workers, Unset):
            workers = UNSET
        else:
            workers = AppWorkerRecovery.from_dict(_workers)

        coverage = d.pop("coverage", UNSET)

        app_operational_recovery = cls(
            rollbacks_available=rollbacks_available,
            restarts_available=restarts_available,
            rollbacks_truncated=rollbacks_truncated,
            restarts_truncated=restarts_truncated,
            rollbacks=rollbacks,
            restarts=restarts,
            workers=workers,
            coverage=coverage,
        )

        app_operational_recovery.additional_properties = d
        return app_operational_recovery

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
