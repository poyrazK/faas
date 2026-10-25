from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.exclusive_work_policy_record import ExclusiveWorkPolicyRecord
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    name: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/account/operation-policies/{name}".format(
            name=quote(str(name), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ExclusiveWorkPolicyRecord | Problem | None:
    if response.status_code == 200:
        response_200 = ExclusiveWorkPolicyRecord.from_dict(response.json())

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
) -> Response[ExclusiveWorkPolicyRecord | Problem]:
    """Retire an idle exclusive operation policy while preserving ownership history.

     Requires deploy-write scope and MFA when configured. Pending or running
    operations and existing trigger bindings block retirement with 409.
    Retirement is idempotent, releases the active-policy quota slot, and
    preserves policy identity, operation receipts and ownership generations.
    A retired name cannot be recreated or used to submit new work.

    Args:
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveWorkPolicyRecord | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> ExclusiveWorkPolicyRecord | Problem | None:
    """Retire an idle exclusive operation policy while preserving ownership history.

     Requires deploy-write scope and MFA when configured. Pending or running
    operations and existing trigger bindings block retirement with 409.
    Retirement is idempotent, releases the active-policy quota slot, and
    preserves policy identity, operation receipts and ownership generations.
    A retired name cannot be recreated or used to submit new work.

    Args:
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExclusiveWorkPolicyRecord | Problem
    """

    return sync_detailed(
        name=name,
        client=client,
    ).parsed


async def asyncio_detailed(
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ExclusiveWorkPolicyRecord | Problem]:
    """Retire an idle exclusive operation policy while preserving ownership history.

     Requires deploy-write scope and MFA when configured. Pending or running
    operations and existing trigger bindings block retirement with 409.
    Retirement is idempotent, releases the active-policy quota slot, and
    preserves policy identity, operation receipts and ownership generations.
    A retired name cannot be recreated or used to submit new work.

    Args:
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveWorkPolicyRecord | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> ExclusiveWorkPolicyRecord | Problem | None:
    """Retire an idle exclusive operation policy while preserving ownership history.

     Requires deploy-write scope and MFA when configured. Pending or running
    operations and existing trigger bindings block retirement with 409.
    Retirement is idempotent, releases the active-policy quota slot, and
    preserves policy identity, operation receipts and ownership generations.
    A retired name cannot be recreated or used to submit new work.

    Args:
        name (str):

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
        )
    ).parsed
