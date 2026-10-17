from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.binding_verification_run_summary_status import (
    BindingVerificationRunSummaryStatus,
    check_binding_verification_run_summary_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="BindingVerificationRunSummary")


@_attrs_define
class BindingVerificationRunSummary:
    """Compact retained receipt identity and stored state, with expiry derived at the page observation time. Passed does
    not assert current evidence qualification. No resource output, plans, policy or manifest is included.

    """

    id: UUID
    status: BindingVerificationRunSummaryStatus
    created_at: datetime.datetime
    expires_at: datetime.datetime
    app: str | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    scope: str | Unset = UNSET
    graph_run_id: UUID | Unset = UNSET
    project: str | Unset = UNSET
    environment: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        status: str = self.status

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        app = self.app

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        scope = self.scope

        graph_run_id: str | Unset = UNSET
        if not isinstance(self.graph_run_id, Unset):
            graph_run_id = str(self.graph_run_id)

        project = self.project

        environment = self.environment

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "status": status,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if app is not UNSET:
            field_dict["app"] = app
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if scope is not UNSET:
            field_dict["scope"] = scope
        if graph_run_id is not UNSET:
            field_dict["graph_run_id"] = graph_run_id
        if project is not UNSET:
            field_dict["project"] = project
        if environment is not UNSET:
            field_dict["environment"] = environment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        status = check_binding_verification_run_summary_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        app = d.pop("app", UNSET)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        scope = d.pop("scope", UNSET)

        _graph_run_id = d.pop("graph_run_id", UNSET)
        graph_run_id: UUID | Unset
        if isinstance(_graph_run_id, Unset):
            graph_run_id = UNSET
        else:
            graph_run_id = UUID(_graph_run_id)

        project = d.pop("project", UNSET)

        environment = d.pop("environment", UNSET)

        binding_verification_run_summary = cls(
            id=id,
            status=status,
            created_at=created_at,
            expires_at=expires_at,
            app=app,
            deployment_id=deployment_id,
            scope=scope,
            graph_run_id=graph_run_id,
            project=project,
            environment=environment,
        )

        return binding_verification_run_summary
