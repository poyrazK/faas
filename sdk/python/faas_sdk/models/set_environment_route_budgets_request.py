from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_route import RouteMonitorRoute


T = TypeVar("T", bound="SetEnvironmentRouteBudgetsRequest")


@_attrs_define
class SetEnvironmentRouteBudgetsRequest:
    """Compare-and-swap update of the selected environment route-budget override."""

    app_id: UUID
    request_id: UUID
    expected_revision: int
    expected_app_revision: int
    inherit: bool
    """Requires enabled=false and empty routes; restores current app thresholds."""
    enabled: bool
    routes: list[RouteMonitorRoute]
    project_id: UUID | Unset = UNSET
    environment_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        request_id = str(self.request_id)

        expected_revision = self.expected_revision

        expected_app_revision = self.expected_app_revision

        inherit = self.inherit

        enabled = self.enabled

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        environment_id: str | Unset = UNSET
        if not isinstance(self.environment_id, Unset):
            environment_id = str(self.environment_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "request_id": request_id,
                "expected_revision": expected_revision,
                "expected_app_revision": expected_app_revision,
                "inherit": inherit,
                "enabled": enabled,
                "routes": routes,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_route import RouteMonitorRoute

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        request_id = UUID(d.pop("request_id"))

        expected_revision = d.pop("expected_revision")

        expected_app_revision = d.pop("expected_app_revision")

        inherit = d.pop("inherit")

        enabled = d.pop("enabled")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteMonitorRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        _project_id = d.pop("project_id", UNSET)
        project_id: UUID | Unset
        if isinstance(_project_id, Unset):
            project_id = UNSET
        else:
            project_id = UUID(_project_id)

        _environment_id = d.pop("environment_id", UNSET)
        environment_id: UUID | Unset
        if isinstance(_environment_id, Unset):
            environment_id = UNSET
        else:
            environment_id = UUID(_environment_id)

        set_environment_route_budgets_request = cls(
            app_id=app_id,
            request_id=request_id,
            expected_revision=expected_revision,
            expected_app_revision=expected_app_revision,
            inherit=inherit,
            enabled=enabled,
            routes=routes,
            project_id=project_id,
            environment_id=environment_id,
        )

        return set_environment_route_budgets_request
