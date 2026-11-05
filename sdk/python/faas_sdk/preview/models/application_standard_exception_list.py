from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_exception import ApplicationStandardException


T = TypeVar("T", bound="ApplicationStandardExceptionList")


@_attrs_define
class ApplicationStandardExceptionList:
    """Page of historical exceptions and their status evaluated at the server as_of timestamp."""

    exceptions: list[ApplicationStandardException]
    as_of: datetime.datetime
    next_page_after: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        exceptions = []
        for exceptions_item_data in self.exceptions:
            exceptions_item = exceptions_item_data.to_dict()
            exceptions.append(exceptions_item)

        as_of = self.as_of.isoformat()

        next_page_after: str | Unset = UNSET
        if not isinstance(self.next_page_after, Unset):
            next_page_after = str(self.next_page_after)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "exceptions": exceptions,
                "as_of": as_of,
            }
        )
        if next_page_after is not UNSET:
            field_dict["next_page_after"] = next_page_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_exception import ApplicationStandardException

        d = dict(src_dict)
        exceptions = []
        _exceptions = d.pop("exceptions")
        for exceptions_item_data in _exceptions:
            exceptions_item = ApplicationStandardException.from_dict(exceptions_item_data)

            exceptions.append(exceptions_item)

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        _next_page_after = d.pop("next_page_after", UNSET)
        next_page_after: UUID | Unset
        if isinstance(_next_page_after, Unset):
            next_page_after = UNSET
        else:
            next_page_after = UUID(_next_page_after)

        application_standard_exception_list = cls(
            exceptions=exceptions,
            as_of=as_of,
            next_page_after=next_page_after,
        )

        return application_standard_exception_list
