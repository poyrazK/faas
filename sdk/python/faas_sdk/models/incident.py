from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.incident_source import IncidentSource, check_incident_source
from ..models.incident_status import IncidentStatus, check_incident_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="Incident")


@_attrs_define
class Incident:
    """An alert rule's firing period, a route monitor incident, or a platform incident."""

    id: str
    source: IncidentSource
    status: IncidentStatus
    title: str
    opened_at: datetime.datetime
    app: str | Unset = UNSET
    """The app's slug; absent for account-wide alerts and platform incidents."""
    resolved_at: datetime.datetime | Unset = UNSET
    rule_id: UUID | Unset = UNSET
    """An alert incident's rule."""
    metric: str | Unset = UNSET
    """An alert incident's rule metric."""
    deployment_id: UUID | Unset = UNSET
    """The deployment a route monitor incident watched."""
    impact: str | Unset = UNSET
    """A platform incident's status page impact."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        source: str = self.source

        status: str = self.status

        title = self.title

        opened_at = self.opened_at.isoformat()

        app = self.app

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        rule_id: str | Unset = UNSET
        if not isinstance(self.rule_id, Unset):
            rule_id = str(self.rule_id)

        metric = self.metric

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        impact = self.impact

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "source": source,
                "status": status,
                "title": title,
                "opened_at": opened_at,
            }
        )
        if app is not UNSET:
            field_dict["app"] = app
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if metric is not UNSET:
            field_dict["metric"] = metric
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if impact is not UNSET:
            field_dict["impact"] = impact

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        source = check_incident_source(d.pop("source"))

        status = check_incident_status(d.pop("status"))

        title = d.pop("title")

        opened_at = datetime.datetime.fromisoformat(d.pop("opened_at"))

        app = d.pop("app", UNSET)

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        _rule_id = d.pop("rule_id", UNSET)
        rule_id: UUID | Unset
        if isinstance(_rule_id, Unset):
            rule_id = UNSET
        else:
            rule_id = UUID(_rule_id)

        metric = d.pop("metric", UNSET)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        impact = d.pop("impact", UNSET)

        incident = cls(
            id=id,
            source=source,
            status=status,
            title=title,
            opened_at=opened_at,
            app=app,
            resolved_at=resolved_at,
            rule_id=rule_id,
            metric=metric,
            deployment_id=deployment_id,
            impact=impact,
        )

        incident.additional_properties = d
        return incident

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
