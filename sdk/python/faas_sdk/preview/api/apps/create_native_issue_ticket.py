from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_native_issue_ticket_request import CreateNativeIssueTicketRequest
from ...models.native_issue_ticket_result import NativeIssueTicketResult
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    issue_id: UUID,
    *,
    body: CreateNativeIssueTicketRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/issues/{issue_id}/native-tickets".format(
            slug=quote(str(slug), safe=""),
            issue_id=quote(str(issue_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | NativeIssueTicketResult | Problem | None:
    if response.status_code == 200:
        response_200 = NativeIssueTicketResult.from_dict(response.json())

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

    if response.status_code == 502:
        response_502 = cast(Any, None)
        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | NativeIssueTicketResult | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateNativeIssueTicketRequest,
) -> Response[Any | NativeIssueTicketResult | Problem]:
    """Create or recover a GitHub ticket through the owned app connection.

     Requires Issues entitlement, deploy-write and session MFA. Pins the owned installation and bound
    repository identity. Retains a server receipt before dispatch; reusing request_id with different
    inputs or binding is refused. An unconfirmed dispatch can only recover a marker-verified existing
    ticket after the 15-second dispatch window. A created result means the ticket exists and attachment
    must be retried with identical inputs. At most 100 receipts are retained per original issue.
    Installation Issues write permission is required; credentials remain server-side.

    Args:
        slug (str):
        issue_id (UUID):
        body (CreateNativeIssueTicketRequest): Explicit content and stable operation identity for
            native GitHub ticket creation or recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | NativeIssueTicketResult | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateNativeIssueTicketRequest,
) -> Any | NativeIssueTicketResult | Problem | None:
    """Create or recover a GitHub ticket through the owned app connection.

     Requires Issues entitlement, deploy-write and session MFA. Pins the owned installation and bound
    repository identity. Retains a server receipt before dispatch; reusing request_id with different
    inputs or binding is refused. An unconfirmed dispatch can only recover a marker-verified existing
    ticket after the 15-second dispatch window. A created result means the ticket exists and attachment
    must be retried with identical inputs. At most 100 receipts are retained per original issue.
    Installation Issues write permission is required; credentials remain server-side.

    Args:
        slug (str):
        issue_id (UUID):
        body (CreateNativeIssueTicketRequest): Explicit content and stable operation identity for
            native GitHub ticket creation or recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | NativeIssueTicketResult | Problem
    """

    return sync_detailed(
        slug=slug,
        issue_id=issue_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateNativeIssueTicketRequest,
) -> Response[Any | NativeIssueTicketResult | Problem]:
    """Create or recover a GitHub ticket through the owned app connection.

     Requires Issues entitlement, deploy-write and session MFA. Pins the owned installation and bound
    repository identity. Retains a server receipt before dispatch; reusing request_id with different
    inputs or binding is refused. An unconfirmed dispatch can only recover a marker-verified existing
    ticket after the 15-second dispatch window. A created result means the ticket exists and attachment
    must be retried with identical inputs. At most 100 receipts are retained per original issue.
    Installation Issues write permission is required; credentials remain server-side.

    Args:
        slug (str):
        issue_id (UUID):
        body (CreateNativeIssueTicketRequest): Explicit content and stable operation identity for
            native GitHub ticket creation or recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | NativeIssueTicketResult | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateNativeIssueTicketRequest,
) -> Any | NativeIssueTicketResult | Problem | None:
    """Create or recover a GitHub ticket through the owned app connection.

     Requires Issues entitlement, deploy-write and session MFA. Pins the owned installation and bound
    repository identity. Retains a server receipt before dispatch; reusing request_id with different
    inputs or binding is refused. An unconfirmed dispatch can only recover a marker-verified existing
    ticket after the 15-second dispatch window. A created result means the ticket exists and attachment
    must be retried with identical inputs. At most 100 receipts are retained per original issue.
    Installation Issues write permission is required; credentials remain server-side.

    Args:
        slug (str):
        issue_id (UUID):
        body (CreateNativeIssueTicketRequest): Explicit content and stable operation identity for
            native GitHub ticket creation or recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | NativeIssueTicketResult | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            issue_id=issue_id,
            client=client,
            body=body,
        )
    ).parsed
