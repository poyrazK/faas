from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.hosting_route_check_set import HostingRouteCheckSet


T = TypeVar("T", bound="DeploymentHostingVerification")


@_attrs_define
class DeploymentHostingVerification:
    """Candidate hosting verification recovery progress. last_error_code distinguishes publication, gateway, transport and
    candidate-evidence failures; a transport failure does not attribute blame to the app or platform. retry_not_before
    is an eligibility floor, not a promised delivery time; completed_at means the attempt finished, while the hosting
    receipt records its verdict.

    """

    started_at: datetime.datetime
    deadline_at: datetime.datetime
    attempts: int
    last_error_code: str | Unset = UNSET
    last_route_checks: HostingRouteCheckSet | Unset = UNSET
    """API route checks from the app's imported OpenAPI document. document_sha256 binds them to that exact
    document."""
    retry_not_before: datetime.datetime | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        started_at = self.started_at.isoformat()

        deadline_at = self.deadline_at.isoformat()

        attempts = self.attempts

        last_error_code = self.last_error_code

        last_route_checks: dict[str, Any] | Unset = UNSET
        if not isinstance(self.last_route_checks, Unset):
            last_route_checks = self.last_route_checks.to_dict()

        retry_not_before: str | Unset = UNSET
        if not isinstance(self.retry_not_before, Unset):
            retry_not_before = self.retry_not_before.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "started_at": started_at,
                "deadline_at": deadline_at,
                "attempts": attempts,
            }
        )
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code
        if last_route_checks is not UNSET:
            field_dict["last_route_checks"] = last_route_checks
        if retry_not_before is not UNSET:
            field_dict["retry_not_before"] = retry_not_before
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.hosting_route_check_set import HostingRouteCheckSet

        d = dict(src_dict)
        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        attempts = d.pop("attempts")

        last_error_code = d.pop("last_error_code", UNSET)

        _last_route_checks = d.pop("last_route_checks", UNSET)
        last_route_checks: HostingRouteCheckSet | Unset
        if isinstance(_last_route_checks, Unset):
            last_route_checks = UNSET
        else:
            last_route_checks = HostingRouteCheckSet.from_dict(_last_route_checks)

        _retry_not_before = d.pop("retry_not_before", UNSET)
        retry_not_before: datetime.datetime | Unset
        if isinstance(_retry_not_before, Unset):
            retry_not_before = UNSET
        else:
            retry_not_before = datetime.datetime.fromisoformat(_retry_not_before)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        deployment_hosting_verification = cls(
            started_at=started_at,
            deadline_at=deadline_at,
            attempts=attempts,
            last_error_code=last_error_code,
            last_route_checks=last_route_checks,
            retry_not_before=retry_not_before,
            completed_at=completed_at,
        )

        deployment_hosting_verification.additional_properties = d
        return deployment_hosting_verification

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
