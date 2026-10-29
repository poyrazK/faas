from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.exclusive_work_policy_record import ExclusiveWorkPolicyRecord
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    name: str,
    *,
    body: Any,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/account/operation-policies/{name}".format(
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ExclusiveWorkPolicyRecord | Problem | None:
    if response.status_code == 200:
        response_200 = ExclusiveWorkPolicyRecord.from_dict(response.json())

        return response_200

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ExclusiveWorkPolicyRecord | Problem]:
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
    body: Any,
    idempotency_key: str | Unset = UNSET,
) -> Response[ExclusiveWorkPolicyRecord | Problem]:
    """Create or revise a named exclusive operation policy.

     The policy explicitly chooses account or platform-tenant scope and
    queue, reject, or join_existing contention. Member app IDs and the
    optional project environment are validated against this account.
    Account and customer identity are derived from authentication; the
    business key never selects a security scope.

    Args:
        name (str):
        idempotency_key (str | Unset):
        body (Any): Account-owned policy for managed exclusive operations. Apps and selected Jobs
            share the account policy namespace; Jobs require account scope and cannot use an app
            project environment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveWorkPolicyRecord | Problem]
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
    body: Any,
    idempotency_key: str | Unset = UNSET,
) -> ExclusiveWorkPolicyRecord | Problem | None:
    """Create or revise a named exclusive operation policy.

     The policy explicitly chooses account or platform-tenant scope and
    queue, reject, or join_existing contention. Member app IDs and the
    optional project environment are validated against this account.
    Account and customer identity are derived from authentication; the
    business key never selects a security scope.

    Args:
        name (str):
        idempotency_key (str | Unset):
        body (Any): Account-owned policy for managed exclusive operations. Apps and selected Jobs
            share the account policy namespace; Jobs require account scope and cannot use an app
            project environment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExclusiveWorkPolicyRecord | Problem
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
    body: Any,
    idempotency_key: str | Unset = UNSET,
) -> Response[ExclusiveWorkPolicyRecord | Problem]:
    """Create or revise a named exclusive operation policy.

     The policy explicitly chooses account or platform-tenant scope and
    queue, reject, or join_existing contention. Member app IDs and the
    optional project environment are validated against this account.
    Account and customer identity are derived from authentication; the
    business key never selects a security scope.

    Args:
        name (str):
        idempotency_key (str | Unset):
        body (Any): Account-owned policy for managed exclusive operations. Apps and selected Jobs
            share the account policy namespace; Jobs require account scope and cannot use an app
            project environment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveWorkPolicyRecord | Problem]
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
    body: Any,
    idempotency_key: str | Unset = UNSET,
) -> ExclusiveWorkPolicyRecord | Problem | None:
    """Create or revise a named exclusive operation policy.

     The policy explicitly chooses account or platform-tenant scope and
    queue, reject, or join_existing contention. Member app IDs and the
    optional project environment are validated against this account.
    Account and customer identity are derived from authentication; the
    business key never selects a security scope.

    Args:
        name (str):
        idempotency_key (str | Unset):
        body (Any): Account-owned policy for managed exclusive operations. Apps and selected Jobs
            share the account policy namespace; Jobs require account scope and cannot use an app
            project environment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExclusiveWorkPolicyRecord | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
