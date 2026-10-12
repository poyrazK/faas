from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.deployment_companions_response_reason import (
    DeploymentCompanionsResponseReason,
    check_deployment_companions_response_reason,
)
from ..models.deployment_companions_response_state import (
    DeploymentCompanionsResponseState,
    check_deployment_companions_response_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.companion_declaration import CompanionDeclaration


T = TypeVar("T", bound="DeploymentCompanionsResponse")


@_attrs_define
class DeploymentCompanionsResponse:
    """Account-owned declarations for one exact deployment; metadata availability is separate from execution health."""

    account_id: str
    app_id: str
    app_slug: str
    deployment_id: str
    scope: str
    """Returned deployment scope; not inferred from a query parameter."""
    state: DeploymentCompanionsResponseState
    """Declaration metadata availability only, never execution health."""
    companions: list[CompanionDeclaration]
    """Known empty is an available empty array. Unavailable metadata is explicitly labelled and must not be
    presented as no configured helpers."""
    reason: DeploymentCompanionsResponseReason | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        account_id = self.account_id

        app_id = self.app_id

        app_slug = self.app_slug

        deployment_id = self.deployment_id

        scope = self.scope

        state: str = self.state

        companions = []
        for companions_item_data in self.companions:
            companions_item = companions_item_data.to_dict()
            companions.append(companions_item)

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "account_id": account_id,
                "app_id": app_id,
                "app_slug": app_slug,
                "deployment_id": deployment_id,
                "scope": scope,
                "state": state,
                "companions": companions,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.companion_declaration import CompanionDeclaration

        d = dict(src_dict)
        account_id = d.pop("account_id")

        app_id = d.pop("app_id")

        app_slug = d.pop("app_slug")

        deployment_id = d.pop("deployment_id")

        scope = d.pop("scope")

        state = check_deployment_companions_response_state(d.pop("state"))

        companions = []
        _companions = d.pop("companions")
        for companions_item_data in _companions:
            companions_item = CompanionDeclaration.from_dict(companions_item_data)

            companions.append(companions_item)

        _reason = d.pop("reason", UNSET)
        reason: DeploymentCompanionsResponseReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_deployment_companions_response_reason(_reason)

        deployment_companions_response = cls(
            account_id=account_id,
            app_id=app_id,
            app_slug=app_slug,
            deployment_id=deployment_id,
            scope=scope,
            state=state,
            companions=companions,
            reason=reason,
        )

        return deployment_companions_response
