from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_verification_run_status import BindingVerificationRunStatus, check_binding_verification_run_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_binding_inventory import AppBindingInventory
    from ..models.binding_release_policy import BindingReleasePolicy
    from ..models.binding_verification_qualification import BindingVerificationQualification
    from ..models.binding_verification_run_step import BindingVerificationRunStep


T = TypeVar("T", bound="BindingVerificationRun")


@_attrs_define
class BindingVerificationRun:
    """Public frozen review with no credentials, private probe pins or stdout. Status is an observation; traffic changes
    still require current atomic policy checks.

    """

    id: UUID
    app: str
    deployment_id: UUID
    scope: str
    status: BindingVerificationRunStatus
    created_at: datetime.datetime
    expires_at: datetime.datetime
    object_storage_write: bool
    queue_publish: bool
    review_digest: str
    policy: BindingReleasePolicy
    """Stored binding verification requirements and revision for an app deployment scope; unconfigured scopes
    default to off."""
    inventory: AppBindingInventory
    """Best-effort app binding metadata with explicit completeness and sanitized section issues."""
    steps: list[BindingVerificationRunStep]
    qualification: BindingVerificationQualification | Unset = UNSET
    """Server-confirmed temporary qualification conditions for the original receipt. No admission or release
    authority; wait within its original expiry and recheck."""
    max_concurrency: int | Unset = 2
    """Frozen shared admission budget; omitted values use two. Graph members share the root budget."""
    graph_run_id: UUID | Unset = UNSET
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app = self.app

        deployment_id = str(self.deployment_id)

        scope = self.scope

        status: str = self.status

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        object_storage_write = self.object_storage_write

        queue_publish = self.queue_publish

        review_digest = self.review_digest

        policy = self.policy.to_dict()

        inventory = self.inventory.to_dict()

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        qualification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.qualification, Unset):
            qualification = self.qualification.to_dict()

        max_concurrency = self.max_concurrency

        graph_run_id: str | Unset = UNSET
        if not isinstance(self.graph_run_id, Unset):
            graph_run_id = str(self.graph_run_id)

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app": app,
                "deployment_id": deployment_id,
                "scope": scope,
                "status": status,
                "created_at": created_at,
                "expires_at": expires_at,
                "object_storage_write": object_storage_write,
                "queue_publish": queue_publish,
                "review_digest": review_digest,
                "policy": policy,
                "inventory": inventory,
                "steps": steps,
            }
        )
        if qualification is not UNSET:
            field_dict["qualification"] = qualification
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if graph_run_id is not UNSET:
            field_dict["graph_run_id"] = graph_run_id
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_binding_inventory import AppBindingInventory
        from ..models.binding_release_policy import BindingReleasePolicy
        from ..models.binding_verification_qualification import BindingVerificationQualification
        from ..models.binding_verification_run_step import BindingVerificationRunStep

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app = d.pop("app")

        deployment_id = UUID(d.pop("deployment_id"))

        scope = d.pop("scope")

        status = check_binding_verification_run_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        object_storage_write = d.pop("object_storage_write")

        queue_publish = d.pop("queue_publish")

        review_digest = d.pop("review_digest")

        policy = BindingReleasePolicy.from_dict(d.pop("policy"))

        inventory = AppBindingInventory.from_dict(d.pop("inventory"))

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = BindingVerificationRunStep.from_dict(steps_item_data)

            steps.append(steps_item)

        _qualification = d.pop("qualification", UNSET)
        qualification: BindingVerificationQualification | Unset
        if isinstance(_qualification, Unset):
            qualification = UNSET
        else:
            qualification = BindingVerificationQualification.from_dict(_qualification)

        max_concurrency = d.pop("max_concurrency", UNSET)

        _graph_run_id = d.pop("graph_run_id", UNSET)
        graph_run_id: UUID | Unset
        if isinstance(_graph_run_id, Unset):
            graph_run_id = UNSET
        else:
            graph_run_id = UUID(_graph_run_id)

        reason = d.pop("reason", UNSET)

        binding_verification_run = cls(
            id=id,
            app=app,
            deployment_id=deployment_id,
            scope=scope,
            status=status,
            created_at=created_at,
            expires_at=expires_at,
            object_storage_write=object_storage_write,
            queue_publish=queue_publish,
            review_digest=review_digest,
            policy=policy,
            inventory=inventory,
            steps=steps,
            qualification=qualification,
            max_concurrency=max_concurrency,
            graph_run_id=graph_run_id,
            reason=reason,
        )

        binding_verification_run.additional_properties = d
        return binding_verification_run

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
