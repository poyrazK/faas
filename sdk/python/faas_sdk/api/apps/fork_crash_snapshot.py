from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_fork_response import AppForkResponse
from ...models.create_app_fork_request import CreateAppForkRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: CreateAppForkRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/crash-snapshots/{id}/fork".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppForkResponse | Problem | None:
    if response.status_code == 202:
        response_202 = AppForkResponse.from_dict(response.json())

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

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppForkResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateAppForkRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[AppForkResponse | Problem]:
    """Open a ready crash snapshot as a production fork.

     Creates an ADR-732 fork restored from this capture instead of the
    deployment's newest snapshot. Same rules as `POST /v1/apps/{slug}/forks`:
    the key needs `deploy:write` and `secrets:read`, the response carries
    the one-time `access_token`, and the fork counts against the fork
    limits. 409 `crash_capture_not_ready` unless the capture is ready and
    unexpired.

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAppForkRequest | Unset): Body of `POST /v1/apps/{slug}/forks`. Every field is
            optional.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppForkResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateAppForkRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> AppForkResponse | Problem | None:
    """Open a ready crash snapshot as a production fork.

     Creates an ADR-732 fork restored from this capture instead of the
    deployment's newest snapshot. Same rules as `POST /v1/apps/{slug}/forks`:
    the key needs `deploy:write` and `secrets:read`, the response carries
    the one-time `access_token`, and the fork counts against the fork
    limits. 409 `crash_capture_not_ready` unless the capture is ready and
    unexpired.

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAppForkRequest | Unset): Body of `POST /v1/apps/{slug}/forks`. Every field is
            optional.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppForkResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateAppForkRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[AppForkResponse | Problem]:
    """Open a ready crash snapshot as a production fork.

     Creates an ADR-732 fork restored from this capture instead of the
    deployment's newest snapshot. Same rules as `POST /v1/apps/{slug}/forks`:
    the key needs `deploy:write` and `secrets:read`, the response carries
    the one-time `access_token`, and the fork counts against the fork
    limits. 409 `crash_capture_not_ready` unless the capture is ready and
    unexpired.

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAppForkRequest | Unset): Body of `POST /v1/apps/{slug}/forks`. Every field is
            optional.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppForkResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateAppForkRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> AppForkResponse | Problem | None:
    """Open a ready crash snapshot as a production fork.

     Creates an ADR-732 fork restored from this capture instead of the
    deployment's newest snapshot. Same rules as `POST /v1/apps/{slug}/forks`:
    the key needs `deploy:write` and `secrets:read`, the response carries
    the one-time `access_token`, and the fork counts against the fork
    limits. 409 `crash_capture_not_ready` unless the capture is ready and
    unexpired.

    Args:
        slug (str):
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAppForkRequest | Unset): Body of `POST /v1/apps/{slug}/forks`. Every field is
            optional.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppForkResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
