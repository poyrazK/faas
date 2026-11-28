from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operator_runtime_config import OperatorRuntimeConfig
from ...models.problem import Problem
from ...models.rollback_operator_runtime_config_request import RollbackOperatorRuntimeConfigRequest
from ...types import Response


def _get_kwargs(
    key: str,
    *,
    body: RollbackOperatorRuntimeConfigRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/config/{key}/rollback".format(
            key=quote(str(key), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperatorRuntimeConfig | Problem | None:
    if response.status_code == 200:
        response_200 = OperatorRuntimeConfig.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperatorRuntimeConfig | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: RollbackOperatorRuntimeConfigRequest,
) -> Response[OperatorRuntimeConfig | Problem]:
    """Roll back a hot runtime setting to a previous revision

     Applies the selected historical value as a new revision through the
    same zero-downtime hot-apply path as PATCH. Only mutable hot settings
    are eligible. The request is optimistic-concurrency protected and
    the rollback itself is appended to the audit and revision history.

    Args:
        key (str):
        body (RollbackOperatorRuntimeConfigRequest): Request to apply a historical runtime
            configuration revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperatorRuntimeConfig | Problem]
    """

    kwargs = _get_kwargs(
        key=key,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: RollbackOperatorRuntimeConfigRequest,
) -> OperatorRuntimeConfig | Problem | None:
    """Roll back a hot runtime setting to a previous revision

     Applies the selected historical value as a new revision through the
    same zero-downtime hot-apply path as PATCH. Only mutable hot settings
    are eligible. The request is optimistic-concurrency protected and
    the rollback itself is appended to the audit and revision history.

    Args:
        key (str):
        body (RollbackOperatorRuntimeConfigRequest): Request to apply a historical runtime
            configuration revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperatorRuntimeConfig | Problem
    """

    return sync_detailed(
        key=key,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: RollbackOperatorRuntimeConfigRequest,
) -> Response[OperatorRuntimeConfig | Problem]:
    """Roll back a hot runtime setting to a previous revision

     Applies the selected historical value as a new revision through the
    same zero-downtime hot-apply path as PATCH. Only mutable hot settings
    are eligible. The request is optimistic-concurrency protected and
    the rollback itself is appended to the audit and revision history.

    Args:
        key (str):
        body (RollbackOperatorRuntimeConfigRequest): Request to apply a historical runtime
            configuration revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperatorRuntimeConfig | Problem]
    """

    kwargs = _get_kwargs(
        key=key,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: RollbackOperatorRuntimeConfigRequest,
) -> OperatorRuntimeConfig | Problem | None:
    """Roll back a hot runtime setting to a previous revision

     Applies the selected historical value as a new revision through the
    same zero-downtime hot-apply path as PATCH. Only mutable hot settings
    are eligible. The request is optimistic-concurrency protected and
    the rollback itself is appended to the audit and revision history.

    Args:
        key (str):
        body (RollbackOperatorRuntimeConfigRequest): Request to apply a historical runtime
            configuration revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperatorRuntimeConfig | Problem
    """

    return (
        await asyncio_detailed(
            key=key,
            client=client,
            body=body,
        )
    ).parsed
