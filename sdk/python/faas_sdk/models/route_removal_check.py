from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_removal_check_status import RouteRemovalCheckStatus, check_route_removal_check_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_removal_mapping import RouteRemovalMapping
    from ..models.route_removal_policy import RouteRemovalPolicy


T = TypeVar("T", bound="RouteRemovalCheck")


@_attrs_define
class RouteRemovalCheck:
    policy: RouteRemovalPolicy
    candidate_deployment_id: UUID
    status: RouteRemovalCheckStatus
    removed: list[RouteRemovalMapping]
    blockers: list[str]
    earliest_approval_at: datetime.datetime | Unset = UNSET
    """Earliest approval time for the current observed baseline and capture."""
    approval_valid_until: datetime.datetime | Unset = UNSET
    """Expiry of the currently valid matching approval."""
    next_actions: list[str] | Unset = UNSET
    """Recovery guidance for the current check."""
    baseline_contract_sha256: str | Unset = UNSET
    """Authoritative baseline capture digest to pin in an approval request."""
    candidate_contract_sha256: str | Unset = UNSET
    """Authoritative candidate capture digest to pin in an approval request."""
    approval_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy.to_dict()

        candidate_deployment_id = str(self.candidate_deployment_id)

        status: str = self.status

        removed = []
        for removed_item_data in self.removed:
            removed_item = removed_item_data.to_dict()
            removed.append(removed_item)

        blockers = self.blockers

        earliest_approval_at: str | Unset = UNSET
        if not isinstance(self.earliest_approval_at, Unset):
            earliest_approval_at = self.earliest_approval_at.isoformat()

        approval_valid_until: str | Unset = UNSET
        if not isinstance(self.approval_valid_until, Unset):
            approval_valid_until = self.approval_valid_until.isoformat()

        next_actions: list[str] | Unset = UNSET
        if not isinstance(self.next_actions, Unset):
            next_actions = self.next_actions

        baseline_contract_sha256 = self.baseline_contract_sha256

        candidate_contract_sha256 = self.candidate_contract_sha256

        approval_id: str | Unset = UNSET
        if not isinstance(self.approval_id, Unset):
            approval_id = str(self.approval_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy": policy,
                "candidate_deployment_id": candidate_deployment_id,
                "status": status,
                "removed": removed,
                "blockers": blockers,
            }
        )
        if earliest_approval_at is not UNSET:
            field_dict["earliest_approval_at"] = earliest_approval_at
        if approval_valid_until is not UNSET:
            field_dict["approval_valid_until"] = approval_valid_until
        if next_actions is not UNSET:
            field_dict["next_actions"] = next_actions
        if baseline_contract_sha256 is not UNSET:
            field_dict["baseline_contract_sha256"] = baseline_contract_sha256
        if candidate_contract_sha256 is not UNSET:
            field_dict["candidate_contract_sha256"] = candidate_contract_sha256
        if approval_id is not UNSET:
            field_dict["approval_id"] = approval_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_removal_mapping import RouteRemovalMapping
        from ..models.route_removal_policy import RouteRemovalPolicy

        d = dict(src_dict)
        policy = RouteRemovalPolicy.from_dict(d.pop("policy"))

        candidate_deployment_id = UUID(d.pop("candidate_deployment_id"))

        status = check_route_removal_check_status(d.pop("status"))

        removed = []
        _removed = d.pop("removed")
        for removed_item_data in _removed:
            removed_item = RouteRemovalMapping.from_dict(removed_item_data)

            removed.append(removed_item)

        blockers = cast(list[str], d.pop("blockers"))

        _earliest_approval_at = d.pop("earliest_approval_at", UNSET)
        earliest_approval_at: datetime.datetime | Unset
        if isinstance(_earliest_approval_at, Unset):
            earliest_approval_at = UNSET
        else:
            earliest_approval_at = datetime.datetime.fromisoformat(_earliest_approval_at)

        _approval_valid_until = d.pop("approval_valid_until", UNSET)
        approval_valid_until: datetime.datetime | Unset
        if isinstance(_approval_valid_until, Unset):
            approval_valid_until = UNSET
        else:
            approval_valid_until = datetime.datetime.fromisoformat(_approval_valid_until)

        next_actions = cast(list[str], d.pop("next_actions", UNSET))

        baseline_contract_sha256 = d.pop("baseline_contract_sha256", UNSET)

        candidate_contract_sha256 = d.pop("candidate_contract_sha256", UNSET)

        _approval_id = d.pop("approval_id", UNSET)
        approval_id: UUID | Unset
        if isinstance(_approval_id, Unset):
            approval_id = UNSET
        else:
            approval_id = UUID(_approval_id)

        route_removal_check = cls(
            policy=policy,
            candidate_deployment_id=candidate_deployment_id,
            status=status,
            removed=removed,
            blockers=blockers,
            earliest_approval_at=earliest_approval_at,
            approval_valid_until=approval_valid_until,
            next_actions=next_actions,
            baseline_contract_sha256=baseline_contract_sha256,
            candidate_contract_sha256=candidate_contract_sha256,
            approval_id=approval_id,
        )

        route_removal_check.additional_properties = d
        return route_removal_check

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
