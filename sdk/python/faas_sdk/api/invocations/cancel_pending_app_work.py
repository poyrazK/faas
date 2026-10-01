from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.cancel_pending_work_request import CancelPendingWorkRequest
from ...models.cancel_pending_work_response import CancelPendingWorkResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: CancelPendingWorkRequest,
    environment: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/work-policies/{name}/cancel-pending".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CancelPendingWorkResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CancelPendingWorkResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[CancelPendingWorkResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: CancelPendingWorkRequest,
    environment: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[CancelPendingWorkResponse | Problem]:
    """Cancel pending work for one policy and application key.

     Running work continues. A repeated Idempotency-Key returns the original receipt and does not cancel
    newer work. A stage selection returns invocation_environment_work_isolation_unavailable (409) before
    touching production lanes or cancellation receipts.

    Args:
        slug (str):
        name (str):
        environment (str | Unset):
        idempotency_key (str | Unset):
        body (CancelPendingWorkRequest): Typed application key identifying pending work in one
            policy lane.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CancelPendingWorkResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
        environment=environment,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: CancelPendingWorkRequest,
    environment: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> CancelPendingWorkResponse | Problem | None:
    """Cancel pending work for one policy and application key.

     Running work continues. A repeated Idempotency-Key returns the original receipt and does not cancel
    newer work. A stage selection returns invocation_environment_work_isolation_unavailable (409) before
    touching production lanes or cancellation receipts.

    Args:
        slug (str):
        name (str):
        environment (str | Unset):
        idempotency_key (str | Unset):
        body (CancelPendingWorkRequest): Typed application key identifying pending work in one
            policy lane.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CancelPendingWorkResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
        environment=environment,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: CancelPendingWorkRequest,
    environment: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[CancelPendingWorkResponse | Problem]:
    """Cancel pending work for one policy and application key.

     Running work continues. A repeated Idempotency-Key returns the original receipt and does not cancel
    newer work. A stage selection returns invocation_environment_work_isolation_unavailable (409) before
    touching production lanes or cancellation receipts.

    Args:
        slug (str):
        name (str):
        environment (str | Unset):
        idempotency_key (str | Unset):
        body (CancelPendingWorkRequest): Typed application key identifying pending work in one
            policy lane.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CancelPendingWorkResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
        environment=environment,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: CancelPendingWorkRequest,
    environment: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> CancelPendingWorkResponse | Problem | None:
    """Cancel pending work for one policy and application key.

     Running work continues. A repeated Idempotency-Key returns the original receipt and does not cancel
    newer work. A stage selection returns invocation_environment_work_isolation_unavailable (409) before
    touching production lanes or cancellation receipts.

    Args:
        slug (str):
        name (str):
        environment (str | Unset):
        idempotency_key (str | Unset):
        body (CancelPendingWorkRequest): Typed application key identifying pending work in one
            policy lane.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CancelPendingWorkResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
            environment=environment,
            idempotency_key=idempotency_key,
        )
    ).parsed
