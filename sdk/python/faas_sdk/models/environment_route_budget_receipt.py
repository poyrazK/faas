from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_route_budget_receipt_budget_source import (
    EnvironmentRouteBudgetReceiptBudgetSource,
    check_environment_route_budget_receipt_budget_source,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentRouteBudgetReceipt")


@_attrs_define
class EnvironmentRouteBudgetReceipt:
    """Original durable policy-intent receipt. Request fingerprints bind exact bodies but grant no write authority. No
    configuration values are exposed.

    """

    request_id: UUID
    request_hash: str
    app_id: UUID
    scope: str
    revision: int
    app_revision: int
    budget_source: EnvironmentRouteBudgetReceiptBudgetSource
    project_id: UUID | Unset = UNSET
    environment_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        request_hash = self.request_hash

        app_id = str(self.app_id)

        scope = self.scope

        revision = self.revision

        app_revision = self.app_revision

        budget_source: str = self.budget_source

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        environment_id: str | Unset = UNSET
        if not isinstance(self.environment_id, Unset):
            environment_id = str(self.environment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "request_hash": request_hash,
                "app_id": app_id,
                "scope": scope,
                "revision": revision,
                "app_revision": app_revision,
                "budget_source": budget_source,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        request_hash = d.pop("request_hash")

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        revision = d.pop("revision")

        app_revision = d.pop("app_revision")

        budget_source = check_environment_route_budget_receipt_budget_source(d.pop("budget_source"))

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

        environment_route_budget_receipt = cls(
            request_id=request_id,
            request_hash=request_hash,
            app_id=app_id,
            scope=scope,
            revision=revision,
            app_revision=app_revision,
            budget_source=budget_source,
            project_id=project_id,
            environment_id=environment_id,
        )

        environment_route_budget_receipt.additional_properties = d
        return environment_route_budget_receipt

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
