from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.dry_run_app_open_api_body import DryRunAppOpenAPIBody
from ...models.dry_run_app_open_api_response_200 import DryRunAppOpenAPIResponse200
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: DryRunAppOpenAPIBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/openapi/dry-run".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | DryRunAppOpenAPIResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = DryRunAppOpenAPIResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 413:
        response_413 = cast(Any, None)
        return response_413

    if response.status_code == 422:
        response_422 = cast(Any, None)
        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | DryRunAppOpenAPIResponse200 | Problem]:
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
    body: DryRunAppOpenAPIBody,
) -> Response[Any | DryRunAppOpenAPIResponse200 | Problem]:
    """Read-only preview of edge-rule suggestions for an imported doc.

     POST-only (the body IS the import body). Same auth chain
    as the GET surface minus the MFA requirement (read-only).
    Validates the doc and walks paths, emitting one
    EdgeRuleSuggestion per (path, method) pair NOT already
    covered by an existing validate edge rule. Empty array
    when the doc is fully covered.

    Customer pastes each suggestion's Path + Methods + Kind
    + Action back into the existing create-edge-rule endpoint
    (item #2 D3). Does NOT persist; does NOT emit pg_notify.

    Args:
        slug (str):
        body (DryRunAppOpenAPIBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | DryRunAppOpenAPIResponse200 | Problem]
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
    client: AuthenticatedClient | Client,
    body: DryRunAppOpenAPIBody,
) -> Any | DryRunAppOpenAPIResponse200 | Problem | None:
    """Read-only preview of edge-rule suggestions for an imported doc.

     POST-only (the body IS the import body). Same auth chain
    as the GET surface minus the MFA requirement (read-only).
    Validates the doc and walks paths, emitting one
    EdgeRuleSuggestion per (path, method) pair NOT already
    covered by an existing validate edge rule. Empty array
    when the doc is fully covered.

    Customer pastes each suggestion's Path + Methods + Kind
    + Action back into the existing create-edge-rule endpoint
    (item #2 D3). Does NOT persist; does NOT emit pg_notify.

    Args:
        slug (str):
        body (DryRunAppOpenAPIBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | DryRunAppOpenAPIResponse200 | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DryRunAppOpenAPIBody,
) -> Response[Any | DryRunAppOpenAPIResponse200 | Problem]:
    """Read-only preview of edge-rule suggestions for an imported doc.

     POST-only (the body IS the import body). Same auth chain
    as the GET surface minus the MFA requirement (read-only).
    Validates the doc and walks paths, emitting one
    EdgeRuleSuggestion per (path, method) pair NOT already
    covered by an existing validate edge rule. Empty array
    when the doc is fully covered.

    Customer pastes each suggestion's Path + Methods + Kind
    + Action back into the existing create-edge-rule endpoint
    (item #2 D3). Does NOT persist; does NOT emit pg_notify.

    Args:
        slug (str):
        body (DryRunAppOpenAPIBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | DryRunAppOpenAPIResponse200 | Problem]
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
    client: AuthenticatedClient | Client,
    body: DryRunAppOpenAPIBody,
) -> Any | DryRunAppOpenAPIResponse200 | Problem | None:
    """Read-only preview of edge-rule suggestions for an imported doc.

     POST-only (the body IS the import body). Same auth chain
    as the GET surface minus the MFA requirement (read-only).
    Validates the doc and walks paths, emitting one
    EdgeRuleSuggestion per (path, method) pair NOT already
    covered by an existing validate edge rule. Empty array
    when the doc is fully covered.

    Customer pastes each suggestion's Path + Methods + Kind
    + Action back into the existing create-edge-rule endpoint
    (item #2 D3). Does NOT persist; does NOT emit pg_notify.

    Args:
        slug (str):
        body (DryRunAppOpenAPIBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | DryRunAppOpenAPIResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
