from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_retry_policy import ExecutionRetryPolicy
    from ..models.execution_task_batch_item import ExecutionTaskBatchItem


T = TypeVar("T", bound="ExecutionTaskBatch")


@_attrs_define
class ExecutionTaskBatch:
    """Expands into independent steps for one immutable task version. Cannot be combined with steps. Item labels must be
    unique. Account and customer/task budgets still apply to every physical attempt.

    """

    name: str
    version: int
    items: list[ExecutionTaskBatchItem]
    customer_ref: str | Unset = UNSET
    retry: ExecutionRetryPolicy | Unset = UNSET
    """ Multiple attempts require safe_to_repeat=true. Only restore_failed, execution_transport_failed and
    lease_expired are retried. Each attempt has a separate Run and budget reservation; external side effects may
    repeat. """

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        version = self.version

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        customer_ref = self.customer_ref

        retry: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry, Unset):
            retry = self.retry.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "version": version,
                "items": items,
            }
        )
        if customer_ref is not UNSET:
            field_dict["customer_ref"] = customer_ref
        if retry is not UNSET:
            field_dict["retry"] = retry

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_retry_policy import ExecutionRetryPolicy
        from ..models.execution_task_batch_item import ExecutionTaskBatchItem

        d = dict(src_dict)
        name = d.pop("name")

        version = d.pop("version")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ExecutionTaskBatchItem.from_dict(items_item_data)

            items.append(items_item)

        customer_ref = d.pop("customer_ref", UNSET)

        _retry = d.pop("retry", UNSET)
        retry: ExecutionRetryPolicy | Unset
        if isinstance(_retry, Unset):
            retry = UNSET
        else:
            retry = ExecutionRetryPolicy.from_dict(_retry)

        execution_task_batch = cls(
            name=name,
            version=version,
            items=items,
            customer_ref=customer_ref,
            retry=retry,
        )

        return execution_task_batch
