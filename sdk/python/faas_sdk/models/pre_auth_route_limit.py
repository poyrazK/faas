from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.pre_auth_route_limit_coordination import (
    PreAuthRouteLimitCoordination,
    check_pre_auth_route_limit_coordination,
)
from ..models.pre_auth_route_limit_method import PreAuthRouteLimitMethod, check_pre_auth_route_limit_method
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.pre_auth_failed_response_limit import PreAuthFailedResponseLimit


T = TypeVar("T", bound="PreAuthRouteLimit")


@_attrs_define
class PreAuthRouteLimit:
    """Optional stricter per-source limit for one public method and path, with optional shared request coordination and a
    response-based failure budget.

    """

    method: PreAuthRouteLimitMethod
    path: str
    """Canonical absolute public URL path with no query, fragment, or percent encoding. Matching is exact after
    normalizing the decoded request path."""
    requests_per_second: int
    burst: int
    coordination: PreAuthRouteLimitCoordination | Unset = "local"
    """Optional shared request budget across gateway replicas. Defaults to local. Central mode uses 1,024 opaque
    source shards per exact route; collisions share allowance. On database errors it falls back to the replica-local
    bucket. Failed-response budgets remain local."""
    failed_responses: PreAuthFailedResponseLimit | Unset = UNSET
    """Optional per-source budget spent only by selected proxied application 4xx responses. When statuses is
    omitted, 401 and 403 are counted. In enforce mode, subsequent requests are rejected before authentication and
    wake after this budget is exhausted."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        requests_per_second = self.requests_per_second

        burst = self.burst

        coordination: str | Unset = UNSET
        if not isinstance(self.coordination, Unset):
            coordination = self.coordination

        failed_responses: dict[str, Any] | Unset = UNSET
        if not isinstance(self.failed_responses, Unset):
            failed_responses = self.failed_responses.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "requests_per_second": requests_per_second,
                "burst": burst,
            }
        )
        if coordination is not UNSET:
            field_dict["coordination"] = coordination
        if failed_responses is not UNSET:
            field_dict["failed_responses"] = failed_responses

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.pre_auth_failed_response_limit import PreAuthFailedResponseLimit

        d = dict(src_dict)
        method = check_pre_auth_route_limit_method(d.pop("method"))

        path = d.pop("path")

        requests_per_second = d.pop("requests_per_second")

        burst = d.pop("burst")

        _coordination = d.pop("coordination", UNSET)
        coordination: PreAuthRouteLimitCoordination | Unset
        if isinstance(_coordination, Unset):
            coordination = UNSET
        else:
            coordination = check_pre_auth_route_limit_coordination(_coordination)

        _failed_responses = d.pop("failed_responses", UNSET)
        failed_responses: PreAuthFailedResponseLimit | Unset
        if isinstance(_failed_responses, Unset):
            failed_responses = UNSET
        else:
            failed_responses = PreAuthFailedResponseLimit.from_dict(_failed_responses)

        pre_auth_route_limit = cls(
            method=method,
            path=path,
            requests_per_second=requests_per_second,
            burst=burst,
            coordination=coordination,
            failed_responses=failed_responses,
        )

        pre_auth_route_limit.additional_properties = d
        return pre_auth_route_limit

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
