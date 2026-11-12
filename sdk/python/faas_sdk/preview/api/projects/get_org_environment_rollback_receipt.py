from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_rollback_receipt import EnvironmentRollbackReceipt
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
    environment: str,
    promotion: UUID,
    attempt_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/rollback-attempts/{attempt_id}".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            promotion=quote(str(promotion), safe=""),
            attempt_id=quote(str(attempt_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentRollbackReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentRollbackReceipt.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EnvironmentRollbackReceipt | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    slug: str,
    environment: str,
    promotion: UUID,
    attempt_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[EnvironmentRollbackReceipt | Problem]:
    """Read original company environment rollback acceptance.

     Returns an immutable acceptance receipt within the authenticated company project. Current membership
    and original source/target identities remain required; replacement environments cannot inherit it.
    Native restoration and workload health require explicit observations. A missing receipt leaves
    acceptance unresolved, and this read cannot execute a rollback.

    Args:
        organization (str):
        slug (str):
        environment (str):
        promotion (UUID):
        attempt_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentRollbackReceipt | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        environment=environment,
        promotion=promotion,
        attempt_id=attempt_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    environment: str,
    promotion: UUID,
    attempt_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> EnvironmentRollbackReceipt | Problem | None:
    """Read original company environment rollback acceptance.

     Returns an immutable acceptance receipt within the authenticated company project. Current membership
    and original source/target identities remain required; replacement environments cannot inherit it.
    Native restoration and workload health require explicit observations. A missing receipt leaves
    acceptance unresolved, and this read cannot execute a rollback.

    Args:
        organization (str):
        slug (str):
        environment (str):
        promotion (UUID):
        attempt_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentRollbackReceipt | Problem
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        environment=environment,
        promotion=promotion,
        attempt_id=attempt_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    environment: str,
    promotion: UUID,
    attempt_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[EnvironmentRollbackReceipt | Problem]:
    """Read original company environment rollback acceptance.

     Returns an immutable acceptance receipt within the authenticated company project. Current membership
    and original source/target identities remain required; replacement environments cannot inherit it.
    Native restoration and workload health require explicit observations. A missing receipt leaves
    acceptance unresolved, and this read cannot execute a rollback.

    Args:
        organization (str):
        slug (str):
        environment (str):
        promotion (UUID):
        attempt_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentRollbackReceipt | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        environment=environment,
        promotion=promotion,
        attempt_id=attempt_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    environment: str,
    promotion: UUID,
    attempt_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> EnvironmentRollbackReceipt | Problem | None:
    """Read original company environment rollback acceptance.

     Returns an immutable acceptance receipt within the authenticated company project. Current membership
    and original source/target identities remain required; replacement environments cannot inherit it.
    Native restoration and workload health require explicit observations. A missing receipt leaves
    acceptance unresolved, and this read cannot execute a rollback.

    Args:
        organization (str):
        slug (str):
        environment (str):
        promotion (UUID):
        attempt_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentRollbackReceipt | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            environment=environment,
            promotion=promotion,
            attempt_id=attempt_id,
            client=client,
        )
    ).parsed
