from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.exclusive_operation_accepted import ExclusiveOperationAccepted
from ...models.exclusive_operation_request import ExclusiveOperationRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    tenant_id: UUID,
    slug: str,
    *,
    body: ExclusiveOperationRequest,
    idempotency_key: str | Unset = UNSET,
    x_gregale_revision: UUID | Unset = UNSET,
    x_gregale_release: UUID | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    if not isinstance(x_gregale_revision, Unset):
        headers["X-Gregale-Revision"] = x_gregale_revision

    if not isinstance(x_gregale_release, Unset):
        headers["X-Gregale-Release"] = x_gregale_release

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{tenant_id}/apps/{slug}/operations".format(
            tenant_id=quote(str(tenant_id), safe=""),
            slug=quote(str(slug), safe=""),
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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[ExclusiveOperationAccepted | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    tenant_id: UUID,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveOperationRequest,
    idempotency_key: str | Unset = UNSET,
    x_gregale_revision: UUID | Unset = UNSET,
    x_gregale_release: UUID | Unset = UNSET,
) -> Response[ExclusiveOperationAccepted | Problem]:
    """Submit work for an account-authorized platform customer.

     The account credential must own the selected tenant. The tenant ID is checked against account state
    and never inferred from the concurrency key.

    Args:
        tenant_id (UUID):
        slug (str):
        idempotency_key (str | Unset):
        x_gregale_revision (UUID | Unset):
        x_gregale_release (UUID | Unset):
        body (ExclusiveOperationRequest): Work request admitted under a named exclusive-operation
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveOperationAccepted | Problem]
    """

    kwargs = _get_kwargs(
        tenant_id=tenant_id,
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
        x_gregale_revision=x_gregale_revision,
        x_gregale_release=x_gregale_release,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    tenant_id: UUID,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveOperationRequest,
    idempotency_key: str | Unset = UNSET,
    x_gregale_revision: UUID | Unset = UNSET,
    x_gregale_release: UUID | Unset = UNSET,
) -> ExclusiveOperationAccepted | Problem | None:
    """Submit work for an account-authorized platform customer.

     The account credential must own the selected tenant. The tenant ID is checked against account state
    and never inferred from the concurrency key.

    Args:
        tenant_id (UUID):
        slug (str):
        idempotency_key (str | Unset):
        x_gregale_revision (UUID | Unset):
        x_gregale_release (UUID | Unset):
        body (ExclusiveOperationRequest): Work request admitted under a named exclusive-operation
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExclusiveOperationAccepted | Problem
    """

    return sync_detailed(
        tenant_id=tenant_id,
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
        x_gregale_revision=x_gregale_revision,
        x_gregale_release=x_gregale_release,
    ).parsed


async def asyncio_detailed(
    tenant_id: UUID,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveOperationRequest,
    idempotency_key: str | Unset = UNSET,
    x_gregale_revision: UUID | Unset = UNSET,
    x_gregale_release: UUID | Unset = UNSET,
) -> Response[ExclusiveOperationAccepted | Problem]:
    """Submit work for an account-authorized platform customer.

     The account credential must own the selected tenant. The tenant ID is checked against account state
    and never inferred from the concurrency key.

    Args:
        tenant_id (UUID):
        slug (str):
        idempotency_key (str | Unset):
        x_gregale_revision (UUID | Unset):
        x_gregale_release (UUID | Unset):
        body (ExclusiveOperationRequest): Work request admitted under a named exclusive-operation
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveOperationAccepted | Problem]
    """

    kwargs = _get_kwargs(
        tenant_id=tenant_id,
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
        x_gregale_revision=x_gregale_revision,
        x_gregale_release=x_gregale_release,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    tenant_id: UUID,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveOperationRequest,
    idempotency_key: str | Unset = UNSET,
    x_gregale_revision: UUID | Unset = UNSET,
    x_gregale_release: UUID | Unset = UNSET,
) -> ExclusiveOperationAccepted | Problem | None:
    """Submit work for an account-authorized platform customer.

     The account credential must own the selected tenant. The tenant ID is checked against account state
    and never inferred from the concurrency key.

    Args:
        tenant_id (UUID):
        slug (str):
        idempotency_key (str | Unset):
        x_gregale_revision (UUID | Unset):
        x_gregale_release (UUID | Unset):
        body (ExclusiveOperationRequest): Work request admitted under a named exclusive-operation
            policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExclusiveOperationAccepted | Problem
    """

    return (
        await asyncio_detailed(
            tenant_id=tenant_id,
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
            x_gregale_revision=x_gregale_revision,
            x_gregale_release=x_gregale_release,
        )
    ).parsed
