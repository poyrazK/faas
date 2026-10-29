from http import HTTPStatus
from typing import Any, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_storage_usage_report import ObjectStorageUsageReport
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: ObjectStorageUsageReport,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/object-storage/usage-reports",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ObjectStorageUsageReport,
    idempotency_key: str,
) -> Response[Any | Problem]:
    """Import a cumulative provider object storage usage report

     Operator session with recent step-up and Idempotency-Key required. Identical reports are idempotent;
    conflicting or regressing reports are rejected. Automated exporters use the operator-owned
    usage_reports_path backend setting.

    Args:
        idempotency_key (str):
        body (ObjectStorageUsageReport): Authoritative cumulative provider usage for one account,
            backend and UTC month. Missing data is not zero; costs are EUR millicents.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: ObjectStorageUsageReport,
    idempotency_key: str,
) -> Any | Problem | None:
    """Import a cumulative provider object storage usage report

     Operator session with recent step-up and Idempotency-Key required. Identical reports are idempotent;
    conflicting or regressing reports are rejected. Automated exporters use the operator-owned
    usage_reports_path backend setting.

    Args:
        idempotency_key (str):
        body (ObjectStorageUsageReport): Authoritative cumulative provider usage for one account,
            backend and UTC month. Missing data is not zero; costs are EUR millicents.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ObjectStorageUsageReport,
    idempotency_key: str,
) -> Response[Any | Problem]:
    """Import a cumulative provider object storage usage report

     Operator session with recent step-up and Idempotency-Key required. Identical reports are idempotent;
    conflicting or regressing reports are rejected. Automated exporters use the operator-owned
    usage_reports_path backend setting.

    Args:
        idempotency_key (str):
        body (ObjectStorageUsageReport): Authoritative cumulative provider usage for one account,
            backend and UTC month. Missing data is not zero; costs are EUR millicents.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ObjectStorageUsageReport,
    idempotency_key: str,
) -> Any | Problem | None:
    """Import a cumulative provider object storage usage report

     Operator session with recent step-up and Idempotency-Key required. Identical reports are idempotent;
    conflicting or regressing reports are rejected. Automated exporters use the operator-owned
    usage_reports_path backend setting.

    Args:
        idempotency_key (str):
        body (ObjectStorageUsageReport): Authoritative cumulative provider usage for one account,
            backend and UTC month. Missing data is not zero; costs are EUR millicents.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
