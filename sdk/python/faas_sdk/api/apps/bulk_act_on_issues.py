from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.bulk_issue_action_request import BulkIssueActionRequest
from ...models.bulk_issue_action_response import BulkIssueActionResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: BulkIssueActionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/issues/bulk-actions".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | BulkIssueActionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BulkIssueActionResponse.from_dict(response.json())

        return response_200

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
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | BulkIssueActionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: BulkIssueActionRequest,
) -> Response[Any | BulkIssueActionResponse | Problem]:
    """Atomically assign, resolve, ignore, or reopen selected issue groups.

     Requires deploy-write permission and session MFA. Up to 50 distinct issue UUIDs; merged aliases are
    acted on once and return one canonical issue. Any missing issue or invalid action rolls back the
    entire batch. Regrouping is excluded. Supports the standard Idempotency-Key header.

    Args:
        slug (str):
        body (BulkIssueActionRequest): Bounded issue selection and one lifecycle or assignment
            operation to apply atomically.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | BulkIssueActionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: BulkIssueActionRequest,
) -> Any | BulkIssueActionResponse | Problem | None:
    """Atomically assign, resolve, ignore, or reopen selected issue groups.

     Requires deploy-write permission and session MFA. Up to 50 distinct issue UUIDs; merged aliases are
    acted on once and return one canonical issue. Any missing issue or invalid action rolls back the
    entire batch. Regrouping is excluded. Supports the standard Idempotency-Key header.

    Args:
        slug (str):
        body (BulkIssueActionRequest): Bounded issue selection and one lifecycle or assignment
            operation to apply atomically.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | BulkIssueActionResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: BulkIssueActionRequest,
) -> Response[Any | BulkIssueActionResponse | Problem]:
    """Atomically assign, resolve, ignore, or reopen selected issue groups.

     Requires deploy-write permission and session MFA. Up to 50 distinct issue UUIDs; merged aliases are
    acted on once and return one canonical issue. Any missing issue or invalid action rolls back the
    entire batch. Regrouping is excluded. Supports the standard Idempotency-Key header.

    Args:
        slug (str):
        body (BulkIssueActionRequest): Bounded issue selection and one lifecycle or assignment
            operation to apply atomically.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | BulkIssueActionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: BulkIssueActionRequest,
) -> Any | BulkIssueActionResponse | Problem | None:
    """Atomically assign, resolve, ignore, or reopen selected issue groups.

     Requires deploy-write permission and session MFA. Up to 50 distinct issue UUIDs; merged aliases are
    acted on once and return one canonical issue. Any missing issue or invalid action rolls back the
    entire batch. Regrouping is excluded. Supports the standard Idempotency-Key header.

    Args:
        slug (str):
        body (BulkIssueActionRequest): Bounded issue selection and one lifecycle or assignment
            operation to apply atomically.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | BulkIssueActionResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
