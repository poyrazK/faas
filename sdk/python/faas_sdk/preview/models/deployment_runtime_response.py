from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.deployment_runtime_response_status import (
    DeploymentRuntimeResponseStatus,
    check_deployment_runtime_response_status,
)

if TYPE_CHECKING:
    from ..models.runtime_release_response import RuntimeReleaseResponse


T = TypeVar("T", bound="DeploymentRuntimeResponse")


@_attrs_define
class DeploymentRuntimeResponse:
    """Recorded deployment runtime binding and published same-family base candidates."""

    deployment_id: str
    status: DeploymentRuntimeResponseStatus
    reason: str
    current: None | RuntimeReleaseResponse
    releases: list[RuntimeReleaseResponse]

    def to_dict(self) -> dict[str, Any]:
        from ..models.runtime_release_response import RuntimeReleaseResponse

        deployment_id = self.deployment_id

        status: str = self.status

        reason = self.reason

        current: dict[str, Any] | None
        if isinstance(self.current, RuntimeReleaseResponse):
            current = self.current.to_dict()
        else:
            current = self.current

        releases = []
        for releases_item_data in self.releases:
            releases_item = releases_item_data.to_dict()
            releases.append(releases_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "deployment_id": deployment_id,
                "status": status,
                "reason": reason,
                "current": current,
                "releases": releases,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.runtime_release_response import RuntimeReleaseResponse

        d = dict(src_dict)
        deployment_id = d.pop("deployment_id")

        status = check_deployment_runtime_response_status(d.pop("status"))

        reason = d.pop("reason")

        def _parse_current(data: object) -> None | RuntimeReleaseResponse:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                current_type_0 = RuntimeReleaseResponse.from_dict(data)

                return current_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | RuntimeReleaseResponse, data)

        current = _parse_current(d.pop("current"))

        releases = []
        _releases = d.pop("releases")
        for releases_item_data in _releases:
            releases_item = RuntimeReleaseResponse.from_dict(releases_item_data)

            releases.append(releases_item)

        deployment_runtime_response = cls(
            deployment_id=deployment_id,
            status=status,
            reason=reason,
            current=current,
            releases=releases,
        )

        return deployment_runtime_response
