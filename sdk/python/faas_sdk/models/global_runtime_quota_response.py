from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.global_runtime_quota_response_status import (
    GlobalRuntimeQuotaResponseStatus,
    check_global_runtime_quota_response_status,
)

if TYPE_CHECKING:
    from ..models.global_runtime_quota_policy import GlobalRuntimeQuotaPolicy
    from ..models.global_runtime_quota_target_response import GlobalRuntimeQuotaTargetResponse


T = TypeVar("T", bound="GlobalRuntimeQuotaResponse")


@_attrs_define
class GlobalRuntimeQuotaResponse:
    """Directory capacity accounting. Reservation and issuance timestamps do not imply running instances, readiness or
    ingress.

    """

    global_app_id: UUID
    allocation_id: UUID
    generation: int
    policy: GlobalRuntimeQuotaPolicy
    """Canonical tenant plan entitlement snapshot used to bound retained allocations."""
    status: GlobalRuntimeQuotaResponseStatus
    created_at: datetime.datetime
    retirement_id: None | UUID
    retired_at: datetime.datetime | None
    targets: list[GlobalRuntimeQuotaTargetResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        global_app_id = str(self.global_app_id)

        allocation_id = str(self.allocation_id)

        generation = self.generation

        policy = self.policy.to_dict()

        status: str = self.status

        created_at = self.created_at.isoformat()

        retirement_id: None | str
        if isinstance(self.retirement_id, UUID):
            retirement_id = str(self.retirement_id)
        else:
            retirement_id = self.retirement_id

        retired_at: None | str
        if isinstance(self.retired_at, datetime.datetime):
            retired_at = self.retired_at.isoformat()
        else:
            retired_at = self.retired_at

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "global_app_id": global_app_id,
                "allocation_id": allocation_id,
                "generation": generation,
                "policy": policy,
                "status": status,
                "created_at": created_at,
                "retirement_id": retirement_id,
                "retired_at": retired_at,
                "targets": targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.global_runtime_quota_policy import GlobalRuntimeQuotaPolicy
        from ..models.global_runtime_quota_target_response import GlobalRuntimeQuotaTargetResponse

        d = dict(src_dict)
        global_app_id = UUID(d.pop("global_app_id"))

        allocation_id = UUID(d.pop("allocation_id"))

        generation = d.pop("generation")

        policy = GlobalRuntimeQuotaPolicy.from_dict(d.pop("policy"))

        status = check_global_runtime_quota_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_retirement_id(data: object) -> None | UUID:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                retirement_id_type_0 = UUID(data)

                return retirement_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | UUID, data)

        retirement_id = _parse_retirement_id(d.pop("retirement_id"))

        def _parse_retired_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                retired_at_type_0 = datetime.datetime.fromisoformat(data)

                return retired_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        retired_at = _parse_retired_at(d.pop("retired_at"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = GlobalRuntimeQuotaTargetResponse.from_dict(targets_item_data)

            targets.append(targets_item)

        global_runtime_quota_response = cls(
            global_app_id=global_app_id,
            allocation_id=allocation_id,
            generation=generation,
            policy=policy,
            status=status,
            created_at=created_at,
            retirement_id=retirement_id,
            retired_at=retired_at,
            targets=targets,
        )

        global_runtime_quota_response.additional_properties = d
        return global_runtime_quota_response

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
