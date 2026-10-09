from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.app_operational_rollback import AppOperationalRollback
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_operational_rollback import AppOperationalRollback
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

        app_operational_recovery = cls(
            rollbacks_available=rollbacks_available,
            restarts_available=restarts_available,
            rollbacks_truncated=rollbacks_truncated,
            restarts_truncated=restarts_truncated,
            rollbacks=rollbacks,
            restarts=restarts,
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
