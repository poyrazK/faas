from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_lifecycle_history_approval_status import (
    RouteLifecycleHistoryApprovalStatus,
    check_route_lifecycle_history_approval_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteLifecycleHistoryApproval")


@_attrs_define
class RouteLifecycleHistoryApproval:
    id: UUID
    used: bool
    status: RouteLifecycleHistoryApprovalStatus
    status_reason: str
    baseline_deployment_id: str
    candidate_deployment_id: str
    baseline_contract_sha256: str
    candidate_contract_sha256: str
    configuration_sha256: str
    valid_until: datetime.datetime
    graph_ids: list[str]
    invalidated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        used = self.used

        status: str = self.status

        status_reason = self.status_reason

        baseline_deployment_id = self.baseline_deployment_id

        candidate_deployment_id = self.candidate_deployment_id

        baseline_contract_sha256 = self.baseline_contract_sha256

        candidate_contract_sha256 = self.candidate_contract_sha256

        configuration_sha256 = self.configuration_sha256

        valid_until = self.valid_until.isoformat()

        graph_ids = self.graph_ids

        invalidated_at: str | Unset = UNSET
        if not isinstance(self.invalidated_at, Unset):
            invalidated_at = self.invalidated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "used": used,
                "status": status,
                "status_reason": status_reason,
                "baseline_deployment_id": baseline_deployment_id,
                "candidate_deployment_id": candidate_deployment_id,
                "baseline_contract_sha256": baseline_contract_sha256,
                "candidate_contract_sha256": candidate_contract_sha256,
                "configuration_sha256": configuration_sha256,
                "valid_until": valid_until,
                "graph_ids": graph_ids,
            }
        )
        if invalidated_at is not UNSET:
            field_dict["invalidated_at"] = invalidated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        used = d.pop("used")

        status = check_route_lifecycle_history_approval_status(d.pop("status"))

        status_reason = d.pop("status_reason")

        baseline_deployment_id = d.pop("baseline_deployment_id")

        candidate_deployment_id = d.pop("candidate_deployment_id")

        baseline_contract_sha256 = d.pop("baseline_contract_sha256")

        candidate_contract_sha256 = d.pop("candidate_contract_sha256")

        configuration_sha256 = d.pop("configuration_sha256")

        valid_until = datetime.datetime.fromisoformat(d.pop("valid_until"))

        graph_ids = cast(list[str], d.pop("graph_ids"))

        _invalidated_at = d.pop("invalidated_at", UNSET)
        invalidated_at: datetime.datetime | Unset
        if isinstance(_invalidated_at, Unset):
            invalidated_at = UNSET
        else:
            invalidated_at = datetime.datetime.fromisoformat(_invalidated_at)

        route_lifecycle_history_approval = cls(
            id=id,
            used=used,
            status=status,
            status_reason=status_reason,
            baseline_deployment_id=baseline_deployment_id,
            candidate_deployment_id=candidate_deployment_id,
            baseline_contract_sha256=baseline_contract_sha256,
            candidate_contract_sha256=candidate_contract_sha256,
            configuration_sha256=configuration_sha256,
            valid_until=valid_until,
            graph_ids=graph_ids,
            invalidated_at=invalidated_at,
        )

        route_lifecycle_history_approval.additional_properties = d
        return route_lifecycle_history_approval

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
