from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.declared_route import DeclaredRoute


T = TypeVar("T", bound="EnvironmentWorkloadRetirementImpact")


@_attrs_define
class EnvironmentWorkloadRetirementImpact:
    """Review-time snapshot of the scoped routes, consumers, schedule and accepted work affected by retained workload
    removal.

    """

    resource: str
    app_id: UUID
    pending_invocations: int
    in_flight_invocations: int
    queued_tasks: int
    in_flight_tasks: int
    routes: list[DeclaredRoute] | Unset = UNSET
    job_id: str | Unset = UNSET
    queue_binding_ids: list[str] | Unset = UNSET
    consumer_ids: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        resource = self.resource

        app_id = str(self.app_id)

        pending_invocations = self.pending_invocations

        in_flight_invocations = self.in_flight_invocations

        queued_tasks = self.queued_tasks

        in_flight_tasks = self.in_flight_tasks

        routes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.routes, Unset):
            routes = []
            for routes_item_data in self.routes:
                routes_item = routes_item_data.to_dict()
                routes.append(routes_item)

        job_id = self.job_id

        queue_binding_ids: list[str] | Unset = UNSET
        if not isinstance(self.queue_binding_ids, Unset):
            queue_binding_ids = self.queue_binding_ids

        consumer_ids: list[str] | Unset = UNSET
        if not isinstance(self.consumer_ids, Unset):
            consumer_ids = self.consumer_ids

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "resource": resource,
                "app_id": app_id,
                "pending_invocations": pending_invocations,
                "in_flight_invocations": in_flight_invocations,
                "queued_tasks": queued_tasks,
                "in_flight_tasks": in_flight_tasks,
            }
        )
        if routes is not UNSET:
            field_dict["routes"] = routes
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if queue_binding_ids is not UNSET:
            field_dict["queue_binding_ids"] = queue_binding_ids
        if consumer_ids is not UNSET:
            field_dict["consumer_ids"] = consumer_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.declared_route import DeclaredRoute

        d = dict(src_dict)
        resource = d.pop("resource")

        app_id = UUID(d.pop("app_id"))

        pending_invocations = d.pop("pending_invocations")

        in_flight_invocations = d.pop("in_flight_invocations")

        queued_tasks = d.pop("queued_tasks")

        in_flight_tasks = d.pop("in_flight_tasks")

        _routes = d.pop("routes", UNSET)
        routes: list[DeclaredRoute] | Unset = UNSET
        if _routes is not UNSET:
            routes = []
            for routes_item_data in _routes:
                routes_item = DeclaredRoute.from_dict(routes_item_data)

                routes.append(routes_item)

        job_id = d.pop("job_id", UNSET)

        queue_binding_ids = cast(list[str], d.pop("queue_binding_ids", UNSET))

        consumer_ids = cast(list[str], d.pop("consumer_ids", UNSET))

        environment_workload_retirement_impact = cls(
            resource=resource,
            app_id=app_id,
            pending_invocations=pending_invocations,
            in_flight_invocations=in_flight_invocations,
            queued_tasks=queued_tasks,
            in_flight_tasks=in_flight_tasks,
            routes=routes,
            job_id=job_id,
            queue_binding_ids=queue_binding_ids,
            consumer_ids=consumer_ids,
        )

        environment_workload_retirement_impact.additional_properties = d
        return environment_workload_retirement_impact

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
