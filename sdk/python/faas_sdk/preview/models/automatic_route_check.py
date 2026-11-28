from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.automatic_route_check_freshness import AutomaticRouteCheckFreshness, check_automatic_route_check_freshness
from ..models.automatic_route_check_last_error_code import (
    AutomaticRouteCheckLastErrorCode,
    check_automatic_route_check_last_error_code,
)
from ..models.automatic_route_check_stale_reasons_item import (
    AutomaticRouteCheckStaleReasonsItem,
    check_automatic_route_check_stale_reasons_item,
)
from ..models.automatic_route_check_state import AutomaticRouteCheckState, check_automatic_route_check_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_check_changes import RouteCheckChanges
    from ..models.route_requirements_check import RouteRequirementsCheck


T = TypeVar("T", bound="AutomaticRouteCheck")


@_attrs_define
class AutomaticRouteCheck:
    """Latest bounded deployment verdict and independently computed freshness. Recent completed checks have bounded
    immutable history and finding deltas; history is not current passing evidence. Queue runs after capture or saved
    intent changes; refresh reevaluates policy drift.

    """

    version: int
    app: str
    app_id: UUID
    deployment_id: UUID
    state: AutomaticRouteCheckState
    freshness: AutomaticRouteCheckFreshness
    stale_reasons: list[AutomaticRouteCheckStaleReasonsItem]
    current_requirements_revision: int
    current_requirements_sha256: str
    attempts: int
    queued_at: datetime.datetime
    last_error_code: AutomaticRouteCheckLastErrorCode | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    check_id: UUID | Unset = UNSET
    """Identity of the stored completed check; may refer to a historical verdict while work is pending."""
    changes: RouteCheckChanges | Unset = UNSET
    """Deterministic bounded comparison with exact summary counts. Initial checks and intent changes establish a
    baseline without claiming resolution. Truncated detail never changes summary counts. Previous observations can
    outlive retained history."""
    check: RouteRequirementsCheck | Unset = UNSET
    """Read-only coverage against one captured deployment and current configuration with saved intent provenance.
    Does not persist history or prove runtime behavior."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        app = self.app

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        state: str = self.state

        freshness: str = self.freshness

        stale_reasons = []
        for stale_reasons_item_data in self.stale_reasons:
            stale_reasons_item: str = stale_reasons_item_data
            stale_reasons.append(stale_reasons_item)

        current_requirements_revision = self.current_requirements_revision

        current_requirements_sha256 = self.current_requirements_sha256

        attempts = self.attempts

        queued_at = self.queued_at.isoformat()

        last_error_code: str | Unset = UNSET
        if not isinstance(self.last_error_code, Unset):
            last_error_code = self.last_error_code

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        check_id: str | Unset = UNSET
        if not isinstance(self.check_id, Unset):
            check_id = str(self.check_id)

        changes: dict[str, Any] | Unset = UNSET
        if not isinstance(self.changes, Unset):
            changes = self.changes.to_dict()

        check: dict[str, Any] | Unset = UNSET
        if not isinstance(self.check, Unset):
            check = self.check.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app": app,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "state": state,
                "freshness": freshness,
                "stale_reasons": stale_reasons,
                "current_requirements_revision": current_requirements_revision,
                "current_requirements_sha256": current_requirements_sha256,
                "attempts": attempts,
                "queued_at": queued_at,
            }
        )
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if check_id is not UNSET:
            field_dict["check_id"] = check_id
        if changes is not UNSET:
            field_dict["changes"] = changes
        if check is not UNSET:
            field_dict["check"] = check

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_check_changes import RouteCheckChanges
        from ..models.route_requirements_check import RouteRequirementsCheck

        d = dict(src_dict)
        version = d.pop("version")

        app = d.pop("app")

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        state = check_automatic_route_check_state(d.pop("state"))

        freshness = check_automatic_route_check_freshness(d.pop("freshness"))

        stale_reasons = []
        _stale_reasons = d.pop("stale_reasons")
        for stale_reasons_item_data in _stale_reasons:
            stale_reasons_item = check_automatic_route_check_stale_reasons_item(stale_reasons_item_data)

            stale_reasons.append(stale_reasons_item)

        current_requirements_revision = d.pop("current_requirements_revision")

        current_requirements_sha256 = d.pop("current_requirements_sha256")

        attempts = d.pop("attempts")

        queued_at = datetime.datetime.fromisoformat(d.pop("queued_at"))

        _last_error_code = d.pop("last_error_code", UNSET)
        last_error_code: AutomaticRouteCheckLastErrorCode | Unset
        if isinstance(_last_error_code, Unset):
            last_error_code = UNSET
        else:
            last_error_code = check_automatic_route_check_last_error_code(_last_error_code)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _check_id = d.pop("check_id", UNSET)
        check_id: UUID | Unset
        if isinstance(_check_id, Unset):
            check_id = UNSET
        else:
            check_id = UUID(_check_id)

        _changes = d.pop("changes", UNSET)
        changes: RouteCheckChanges | Unset
        if isinstance(_changes, Unset):
            changes = UNSET
        else:
            changes = RouteCheckChanges.from_dict(_changes)

        _check = d.pop("check", UNSET)
        check: RouteRequirementsCheck | Unset
        if isinstance(_check, Unset):
            check = UNSET
        else:
            check = RouteRequirementsCheck.from_dict(_check)

        automatic_route_check = cls(
            version=version,
            app=app,
            app_id=app_id,
            deployment_id=deployment_id,
            state=state,
            freshness=freshness,
            stale_reasons=stale_reasons,
            current_requirements_revision=current_requirements_revision,
            current_requirements_sha256=current_requirements_sha256,
            attempts=attempts,
            queued_at=queued_at,
            last_error_code=last_error_code,
            next_attempt_at=next_attempt_at,
            checked_at=checked_at,
            check_id=check_id,
            changes=changes,
            check=check,
        )

        automatic_route_check.additional_properties = d
        return automatic_route_check

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
