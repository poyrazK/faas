from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_rollback_auto_undo_status import (
    ProjectRollbackAutoUndoStatus,
    check_project_rollback_auto_undo_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.problem import Problem


T = TypeVar("T", bound="ProjectRollbackAutoUndo")


@_attrs_define
class ProjectRollbackAutoUndo:
    """The trigger and outcome of an automatic undo after a confirmed health failure."""

    status: ProjectRollbackAutoUndoStatus
    trigger_app_id: UUID
    """ Service with the confirmed 5xx failure. """
    trigger: str | Unset = UNSET
    """ Identifies a live_traffic breach. Omitted for a health probe failure. """
    route_generation: int | Unset = UNSET
    """ Applied route generation for the production breach. """
    window_start: datetime.datetime | Unset = UNSET
    """ Start of the original live-traffic breach window. """
    window_end: datetime.datetime | Unset = UNSET
    """ End of the original live-traffic breach window. """
    problem: Problem | Unset = UNSET
    """ RFC 9457 problem+json envelope. The `code` field is the stable
    machine-readable identifier; clients branch on it. `limit` and
    `observed` are populated on quota errors. `docs_url` points the
    user at the next action. Common customer-facing codes and recovery
    guidance are listed in `docs/errors.md`. `billing_portal_url` is
    populated on `code: payment_required` when the customer already has a
    provider subscription and must update it in the provider
    portal. `checkout_url` is populated when a new hosted checkout
    is required. `paddle_checkout_url` is retained as a legacy
    alias for Paddle clients, and `tx_id` carries the provider
    checkout handle when one exists.

    `errors` carries per-field detail (Cloudflare / Stripe shape)
    for 422 sites that emit a list of field-level failures — used
    today by the kind=validate edge rule so a JSON Schema
    rejection renders as a form-field list the dashboard can
    iterate without parsing prose. Optional + omitempty so every
    other problem+json site keeps its existing flat shape unchanged.
     """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        trigger_app_id = str(self.trigger_app_id)

        trigger = self.trigger

        route_generation = self.route_generation

        window_start: str | Unset = UNSET
        if not isinstance(self.window_start, Unset):
            window_start = self.window_start.isoformat()

        window_end: str | Unset = UNSET
        if not isinstance(self.window_end, Unset):
            window_end = self.window_end.isoformat()

        problem: dict[str, Any] | Unset = UNSET
        if not isinstance(self.problem, Unset):
            problem = self.problem.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "trigger_app_id": trigger_app_id,
            }
        )
        if trigger is not UNSET:
            field_dict["trigger"] = trigger
        if route_generation is not UNSET:
            field_dict["route_generation"] = route_generation
        if window_start is not UNSET:
            field_dict["window_start"] = window_start
        if window_end is not UNSET:
            field_dict["window_end"] = window_end
        if problem is not UNSET:
            field_dict["problem"] = problem

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.problem import Problem

        d = dict(src_dict)
        status = check_project_rollback_auto_undo_status(d.pop("status"))

        trigger_app_id = UUID(d.pop("trigger_app_id"))

        trigger = d.pop("trigger", UNSET)

        route_generation = d.pop("route_generation", UNSET)

        _window_start = d.pop("window_start", UNSET)
        window_start: datetime.datetime | Unset
        if isinstance(_window_start, Unset):
            window_start = UNSET
        else:
            window_start = datetime.datetime.fromisoformat(_window_start)

        _window_end = d.pop("window_end", UNSET)
        window_end: datetime.datetime | Unset
        if isinstance(_window_end, Unset):
            window_end = UNSET
        else:
            window_end = datetime.datetime.fromisoformat(_window_end)

        _problem = d.pop("problem", UNSET)
        problem: Problem | Unset
        if isinstance(_problem, Unset):
            problem = UNSET
        else:
            problem = Problem.from_dict(_problem)

        project_rollback_auto_undo = cls(
            status=status,
            trigger_app_id=trigger_app_id,
            trigger=trigger,
            route_generation=route_generation,
            window_start=window_start,
            window_end=window_end,
            problem=problem,
        )

        project_rollback_auto_undo.additional_properties = d
        return project_rollback_auto_undo

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
