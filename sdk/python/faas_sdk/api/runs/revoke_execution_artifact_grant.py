from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.revoke_execution_artifact_grant_response import RevokeExecutionArtifactGrantResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/execution-artifact-grants/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RevokeExecutionArtifactGrantResponse | None:
    if response.status_code == 200:
        response_200 = RevokeExecutionArtifactGrantResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RevokeExecutionArtifactGrantResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | RevokeExecutionArtifactGrantResponse]:
    """Revoke an unredeemed artifact grant.

     Revokes future use of one artifact capability. A Runs-only key may
    revoke grants it created; account-wide principals may revoke any
    account grant. A grant already redeemed cannot be undone, but the
    recipient's new run retains its own encrypted copy. Revocation remains
    available when new Runs admission is disabled.

    Args:
        id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RevokeExecutionArtifactGrantResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> Problem | RevokeExecutionArtifactGrantResponse | None:
    """Revoke an unredeemed artifact grant.

     Revokes future use of one artifact capability. A Runs-only key may
    revoke grants it created; account-wide principals may revoke any
    account grant. A grant already redeemed cannot be undone, but the
    recipient's new run retains its own encrypted copy. Revocation remains
    available when new Runs admission is disabled.

    Args:
        id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RevokeExecutionArtifactGrantResponse
    """

    return sync_detailed(
        id=id,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | RevokeExecutionArtifactGrantResponse]:
    """Revoke an unredeemed artifact grant.

     Revokes future use of one artifact capability. A Runs-only key may
    revoke grants it created; account-wide principals may revoke any
    account grant. A grant already redeemed cannot be undone, but the
    recipient's new run retains its own encrypted copy. Revocation remains
    available when new Runs admission is disabled.

    Args:
        id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RevokeExecutionArtifactGrantResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> Problem | RevokeExecutionArtifactGrantResponse | None:
    """Revoke an unredeemed artifact grant.

     Revokes future use of one artifact capability. A Runs-only key may
    revoke grants it created; account-wide principals may revoke any
    account grant. A grant already redeemed cannot be undone, but the
    recipient's new run retains its own encrypted copy. Revocation remains
    available when new Runs admission is disabled.

    Args:
        id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RevokeExecutionArtifactGrantResponse
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
