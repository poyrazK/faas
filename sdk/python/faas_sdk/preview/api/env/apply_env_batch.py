from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.env_batch_request import EnvBatchRequest
from ...models.env_batch_response import EnvBatchResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: EnvBatchRequest,
    scope: str | Unset = UNSET,
    guard: bool | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["scope"] = scope

    params["guard"] = guard

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/env/batch".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvBatchResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EnvBatchResponse.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[EnvBatchResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnvBatchRequest,
    scope: str | Unset = UNSET,
    guard: bool | Unset = UNSET,
) -> Response[EnvBatchResponse | Problem]:
    """Atomically set and delete non-secret variables in one scope.

     Requires env:write. Compares expected_revision against configuration
    history and live values, then commits all variables, one history revision,
    snapshot invalidation and a value-free replay receipt in one transaction.
    Use revision 0 only for an empty scope with no history. Reuse request_id with the exact
    original body after an uncertain response; the original receipt is
    returned even if later changes advanced the revision. Receipts are kept
    for at least 24 hours. Reusing an ID with a changed body conflicts.
    Revision conflicts require another review and a new request ID.
    Running instances receive values on their next cold wake or fresh restart.

    Args:
        slug (str):
        scope (str | Unset):
        guard (bool | Unset):
        body (EnvBatchRequest): At least one set or delete is required. Keys must be distinct
            across both collections; plan limits bound collection counts and value sizes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvBatchResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
        guard=guard,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnvBatchRequest,
    scope: str | Unset = UNSET,
    guard: bool | Unset = UNSET,
) -> EnvBatchResponse | Problem | None:
    """Atomically set and delete non-secret variables in one scope.

     Requires env:write. Compares expected_revision against configuration
    history and live values, then commits all variables, one history revision,
    snapshot invalidation and a value-free replay receipt in one transaction.
    Use revision 0 only for an empty scope with no history. Reuse request_id with the exact
    original body after an uncertain response; the original receipt is
    returned even if later changes advanced the revision. Receipts are kept
    for at least 24 hours. Reusing an ID with a changed body conflicts.
    Revision conflicts require another review and a new request ID.
    Running instances receive values on their next cold wake or fresh restart.

    Args:
        slug (str):
        scope (str | Unset):
        guard (bool | Unset):
        body (EnvBatchRequest): At least one set or delete is required. Keys must be distinct
            across both collections; plan limits bound collection counts and value sizes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvBatchResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        scope=scope,
        guard=guard,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnvBatchRequest,
    scope: str | Unset = UNSET,
    guard: bool | Unset = UNSET,
) -> Response[EnvBatchResponse | Problem]:
    """Atomically set and delete non-secret variables in one scope.

     Requires env:write. Compares expected_revision against configuration
    history and live values, then commits all variables, one history revision,
    snapshot invalidation and a value-free replay receipt in one transaction.
    Use revision 0 only for an empty scope with no history. Reuse request_id with the exact
    original body after an uncertain response; the original receipt is
    returned even if later changes advanced the revision. Receipts are kept
    for at least 24 hours. Reusing an ID with a changed body conflicts.
    Revision conflicts require another review and a new request ID.
    Running instances receive values on their next cold wake or fresh restart.

    Args:
        slug (str):
        scope (str | Unset):
        guard (bool | Unset):
        body (EnvBatchRequest): At least one set or delete is required. Keys must be distinct
            across both collections; plan limits bound collection counts and value sizes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvBatchResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
        guard=guard,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: EnvBatchRequest,
    scope: str | Unset = UNSET,
    guard: bool | Unset = UNSET,
) -> EnvBatchResponse | Problem | None:
    """Atomically set and delete non-secret variables in one scope.

     Requires env:write. Compares expected_revision against configuration
    history and live values, then commits all variables, one history revision,
    snapshot invalidation and a value-free replay receipt in one transaction.
    Use revision 0 only for an empty scope with no history. Reuse request_id with the exact
    original body after an uncertain response; the original receipt is
    returned even if later changes advanced the revision. Receipts are kept
    for at least 24 hours. Reusing an ID with a changed body conflicts.
    Revision conflicts require another review and a new request ID.
    Running instances receive values on their next cold wake or fresh restart.

    Args:
        slug (str):
        scope (str | Unset):
        guard (bool | Unset):
        body (EnvBatchRequest): At least one set or delete is required. Keys must be distinct
            across both collections; plan limits bound collection counts and value sizes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvBatchResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            scope=scope,
            guard=guard,
        )
    ).parsed
