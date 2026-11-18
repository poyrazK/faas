from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.hosted_task_binding_verification_status import (
    HostedTaskBindingVerificationStatus,
    check_hosted_task_binding_verification_status,
)
from ..models.hosted_task_binding_verification_version import (
    HostedTaskBindingVerificationVersion,
    check_hosted_task_binding_verification_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.hosted_task_binding_result import HostedTaskBindingResult


T = TypeVar("T", bound="HostedTaskBindingVerification")


@_attrs_define
class HostedTaskBindingVerification:
    """Historical private-binding qualification metadata only. No credential values, references, source, database facts,
    owner decisions or execution authority.

    """

    version: HostedTaskBindingVerificationVersion
    account_id: UUID
    intent_id: str
    intent_sha256: str
    review_sha256: str
    binding_sha256: str
    status: HostedTaskBindingVerificationStatus
    checks: list[HostedTaskBindingResult]
    created_at: datetime.datetime
    expires_at: datetime.datetime
    ready_to_apply: bool
    bindings_verified: bool
    owner_authorization_required: bool
    checked_at: datetime.datetime | Unset = UNSET
    valid_until: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        account_id = str(self.account_id)

        intent_id = self.intent_id

        intent_sha256 = self.intent_sha256

        review_sha256 = self.review_sha256

        binding_sha256 = self.binding_sha256

        status: str = self.status

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        ready_to_apply = self.ready_to_apply

        bindings_verified = self.bindings_verified

        owner_authorization_required = self.owner_authorization_required

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        valid_until: str | Unset = UNSET
        if not isinstance(self.valid_until, Unset):
            valid_until = self.valid_until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "account_id": account_id,
                "intent_id": intent_id,
                "intent_sha256": intent_sha256,
                "review_sha256": review_sha256,
                "binding_sha256": binding_sha256,
                "status": status,
                "checks": checks,
                "created_at": created_at,
                "expires_at": expires_at,
                "ready_to_apply": ready_to_apply,
                "bindings_verified": bindings_verified,
                "owner_authorization_required": owner_authorization_required,
            }
        )
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if valid_until is not UNSET:
            field_dict["valid_until"] = valid_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.hosted_task_binding_result import HostedTaskBindingResult

        d = dict(src_dict)
        version = check_hosted_task_binding_verification_version(d.pop("version"))

        account_id = UUID(d.pop("account_id"))

        intent_id = d.pop("intent_id")

        intent_sha256 = d.pop("intent_sha256")

        review_sha256 = d.pop("review_sha256")

        binding_sha256 = d.pop("binding_sha256")

        status = check_hosted_task_binding_verification_status(d.pop("status"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = HostedTaskBindingResult.from_dict(checks_item_data)

            checks.append(checks_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        ready_to_apply = d.pop("ready_to_apply")

        bindings_verified = d.pop("bindings_verified")

        owner_authorization_required = d.pop("owner_authorization_required")

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _valid_until = d.pop("valid_until", UNSET)
        valid_until: datetime.datetime | Unset
        if isinstance(_valid_until, Unset):
            valid_until = UNSET
        else:
            valid_until = datetime.datetime.fromisoformat(_valid_until)

        hosted_task_binding_verification = cls(
            version=version,
            account_id=account_id,
            intent_id=intent_id,
            intent_sha256=intent_sha256,
            review_sha256=review_sha256,
            binding_sha256=binding_sha256,
            status=status,
            checks=checks,
            created_at=created_at,
            expires_at=expires_at,
            ready_to_apply=ready_to_apply,
            bindings_verified=bindings_verified,
            owner_authorization_required=owner_authorization_required,
            checked_at=checked_at,
            valid_until=valid_until,
        )

        return hosted_task_binding_verification
