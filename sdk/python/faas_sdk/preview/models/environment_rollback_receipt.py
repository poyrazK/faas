from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EnvironmentRollbackReceipt")


@_attrs_define
class EnvironmentRollbackReceipt:
    """Immutable original rollback admission evidence. The source/target identities cannot be reassigned; restoration and
    current health remain separate.

    """

    attempt_id: str
    request_hash: str
    promotion_id: UUID
    promotion_hash: str
    project_id: UUID
    from_environment_id: UUID
    environment_id: UUID
    accepted_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempt_id = self.attempt_id

        request_hash = self.request_hash

        promotion_id = str(self.promotion_id)

        promotion_hash = self.promotion_hash

        project_id = str(self.project_id)

        from_environment_id = str(self.from_environment_id)

        environment_id = str(self.environment_id)

        accepted_at = self.accepted_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempt_id": attempt_id,
                "request_hash": request_hash,
                "promotion_id": promotion_id,
                "promotion_hash": promotion_hash,
                "project_id": project_id,
                "from_environment_id": from_environment_id,
                "environment_id": environment_id,
                "accepted_at": accepted_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempt_id = d.pop("attempt_id")

        request_hash = d.pop("request_hash")

        promotion_id = UUID(d.pop("promotion_id"))

        promotion_hash = d.pop("promotion_hash")

        project_id = UUID(d.pop("project_id"))

        from_environment_id = UUID(d.pop("from_environment_id"))

        environment_id = UUID(d.pop("environment_id"))

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        environment_rollback_receipt = cls(
            attempt_id=attempt_id,
            request_hash=request_hash,
            promotion_id=promotion_id,
            promotion_hash=promotion_hash,
            project_id=project_id,
            from_environment_id=from_environment_id,
            environment_id=environment_id,
            accepted_at=accepted_at,
        )

        environment_rollback_receipt.additional_properties = d
        return environment_rollback_receipt

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
