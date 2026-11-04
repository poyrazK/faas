from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.exclusive_job_operation_request import ExclusiveJobOperationRequest
from ...models.exclusive_operation_accepted import ExclusiveOperationAccepted
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    name: str,
    *,
    body: ExclusiveJobOperationRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/jobs/{name}/operations".format(
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ExclusiveOperationAccepted | Problem | None:
    if response.status_code == 202:
        response_202 = ExclusiveOperationAccepted.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ExclusiveOperationAccepted | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveJobOperationRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ExclusiveOperationAccepted | Problem]:
    """Submit a Job run through managed exclusive ownership.

     The account and Job identity come from the authenticated route. The key is namespaced by the
    selected policy and is not an authorization boundary.

    Args:
        name (str):
        idempotency_key (str | Unset):
        body (ExclusiveJobOperationRequest): Job run intent admitted under a named exclusive-
            operation policy. The account and Job identity are derived from the authenticated route.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveOperationAccepted | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveJobOperationRequest,
    idempotency_key: str | Unset = UNSET,
) -> ExclusiveOperationAccepted | Problem | None:
    """Submit a Job run through managed exclusive ownership.

     The account and Job identity come from the authenticated route. The key is namespaced by the
    selected policy and is not an authorization boundary.

    Args:
        name (str):
        idempotency_key (str | Unset):
        body (ExclusiveJobOperationRequest): Job run intent admitted under a named exclusive-
            operation policy. The account and Job identity are derived from the authenticated route.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExclusiveOperationAccepted | Problem
    """

    return sync_detailed(
        name=name,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveJobOperationRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ExclusiveOperationAccepted | Problem]:
    """Submit a Job run through managed exclusive ownership.

     The account and Job identity come from the authenticated route. The key is namespaced by the
    selected policy and is not an authorization boundary.

    Args:
        name (str):
        idempotency_key (str | Unset):
        body (ExclusiveJobOperationRequest): Job run intent admitted under a named exclusive-
            operation policy. The account and Job identity are derived from the authenticated route.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveOperationAccepted | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveJobOperationRequest,
    idempotency_key: str | Unset = UNSET,
) -> ExclusiveOperationAccepted | Problem | None:
    """Submit a Job run through managed exclusive ownership.

     The account and Job identity come from the authenticated route. The key is namespaced by the
    selected policy and is not an authorization boundary.

    Args:
        name (str):
        idempotency_key (str | Unset):
        body (ExclusiveJobOperationRequest): Job run intent admitted under a named exclusive-
            operation policy. The account and Job identity are derived from the authenticated route.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExclusiveOperationAccepted | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
