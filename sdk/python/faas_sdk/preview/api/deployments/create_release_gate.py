from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_release_gate_request import CreateReleaseGateRequest
from ...models.problem import Problem
from ...models.release_gate import ReleaseGate
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateReleaseGateRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/release-gates".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | ReleaseGate | None:
    if response.status_code == 201:
        response_201 = ReleaseGate.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[Problem | ReleaseGate]:
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
    body: CreateReleaseGateRequest,
) -> Response[Problem | ReleaseGate]:
    """Add a release gate.

     An slo gate names one of the app's SLOs; a new_errors gate counts error groups the release produced
    first; a custom_metric gate bounds a custom metric's average; an ai gate fails a release whose AI
    gateway calls got worse than its baseline's. Omitted numbers take Gregale's defaults.

    Args:
        slug (str):
        body (CreateReleaseGateRequest): Adds a release gate. Give exactly one kind's fields;
            omitted numbers take Gregale's defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseGate]
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
    body: CreateReleaseGateRequest,
) -> Problem | ReleaseGate | None:
    """Add a release gate.

     An slo gate names one of the app's SLOs; a new_errors gate counts error groups the release produced
    first; a custom_metric gate bounds a custom metric's average; an ai gate fails a release whose AI
    gateway calls got worse than its baseline's. Omitted numbers take Gregale's defaults.

    Args:
        slug (str):
        body (CreateReleaseGateRequest): Adds a release gate. Give exactly one kind's fields;
            omitted numbers take Gregale's defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseGate
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
    body: CreateReleaseGateRequest,
) -> Response[Problem | ReleaseGate]:
    """Add a release gate.

     An slo gate names one of the app's SLOs; a new_errors gate counts error groups the release produced
    first; a custom_metric gate bounds a custom metric's average; an ai gate fails a release whose AI
    gateway calls got worse than its baseline's. Omitted numbers take Gregale's defaults.

    Args:
        slug (str):
        body (CreateReleaseGateRequest): Adds a release gate. Give exactly one kind's fields;
            omitted numbers take Gregale's defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseGate]
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
    body: CreateReleaseGateRequest,
) -> Problem | ReleaseGate | None:
    """Add a release gate.

     An slo gate names one of the app's SLOs; a new_errors gate counts error groups the release produced
    first; a custom_metric gate bounds a custom metric's average; an ai gate fails a release whose AI
    gateway calls got worse than its baseline's. Omitted numbers take Gregale's defaults.

    Args:
        slug (str):
        body (CreateReleaseGateRequest): Adds a release gate. Give exactly one kind's fields;
            omitted numbers take Gregale's defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseGate
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
