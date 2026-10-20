from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.github_check_update_record_status import (
    GithubCheckUpdateRecordStatus,
    check_github_check_update_record_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="GithubCheckUpdateRecord")


@_attrs_define
class GithubCheckUpdateRecord:
    """Queue metadata for one outbound GitHub Check Run update."""

    deployment_id: UUID
    generation: int
    status: GithubCheckUpdateRecordStatus
    attempts: int
    next_attempt_at: datetime.datetime
    updated_at: datetime.datetime
    last_error: str | Unset = UNSET
    processed_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        generation = self.generation

        status: str = self.status

        attempts = self.attempts

        next_attempt_at = self.next_attempt_at.isoformat()

        updated_at = self.updated_at.isoformat()

        last_error = self.last_error

        processed_at: None | str | Unset
        if isinstance(self.processed_at, Unset):
            processed_at = UNSET
        elif isinstance(self.processed_at, datetime.datetime):
            processed_at = self.processed_at.isoformat()
        else:
            processed_at = self.processed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "generation": generation,
                "status": status,
                "attempts": attempts,
                "next_attempt_at": next_attempt_at,
                "updated_at": updated_at,
            }
        )
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if processed_at is not UNSET:
            field_dict["processed_at"] = processed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        generation = d.pop("generation")

        status = check_github_check_update_record_status(d.pop("status"))

        attempts = d.pop("attempts")

        next_attempt_at = datetime.datetime.fromisoformat(d.pop("next_attempt_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        last_error = d.pop("last_error", UNSET)

        def _parse_processed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                processed_at_type_0 = datetime.datetime.fromisoformat(data)

                return processed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        processed_at = _parse_processed_at(d.pop("processed_at", UNSET))

        github_check_update_record = cls(
            deployment_id=deployment_id,
            generation=generation,
            status=status,
            attempts=attempts,
            next_attempt_at=next_attempt_at,
            updated_at=updated_at,
            last_error=last_error,
            processed_at=processed_at,
        )

        github_check_update_record.additional_properties = d
        return github_check_update_record

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
