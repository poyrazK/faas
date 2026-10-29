from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mint_durable_entity_session_grant_body import MintDurableEntitySessionGrantBody
from ...models.mint_durable_entity_session_grant_response_201 import MintDurableEntitySessionGrantResponse201
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: MintDurableEntitySessionGrantBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/entities/session-grants".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MintDurableEntitySessionGrantResponse201 | Problem | None:
    if response.status_code == 201:
        response_201 = MintDurableEntitySessionGrantResponse201.from_dict(response.json())

        return response_201

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[MintDurableEntitySessionGrantResponse201 | Problem]:
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
    body: MintDurableEntitySessionGrantBody,
) -> Response[MintDurableEntitySessionGrantResponse201 | Problem]:
    """Mint Durable Entity Session Grant.

     Mint durable entity session grant: Opt-in durable-object preview. Requires the configured
    application allowlist and applicable feature gates; production enablement awaits native GCS/KVM
    acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy
    write access for mutation. Reader grace, claims and unknown acknowledgements must be respected.
    Responses are not cacheable. See the durable entity operator guides for the bounded recovery
    contract.

    Args:
        slug (str):
        body (MintDurableEntitySessionGrantBody): Mint durable entity session grant — requestBody
            / content / application/json / schema: Preview mutation input with server-side size, scope
            and revision validation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MintDurableEntitySessionGrantResponse201 | Problem]
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
    body: MintDurableEntitySessionGrantBody,
) -> MintDurableEntitySessionGrantResponse201 | Problem | None:
    """Mint Durable Entity Session Grant.

     Mint durable entity session grant: Opt-in durable-object preview. Requires the configured
    application allowlist and applicable feature gates; production enablement awaits native GCS/KVM
    acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy
    write access for mutation. Reader grace, claims and unknown acknowledgements must be respected.
    Responses are not cacheable. See the durable entity operator guides for the bounded recovery
    contract.

    Args:
        slug (str):
        body (MintDurableEntitySessionGrantBody): Mint durable entity session grant — requestBody
            / content / application/json / schema: Preview mutation input with server-side size, scope
            and revision validation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MintDurableEntitySessionGrantResponse201 | Problem
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
    body: MintDurableEntitySessionGrantBody,
) -> Response[MintDurableEntitySessionGrantResponse201 | Problem]:
    """Mint Durable Entity Session Grant.

     Mint durable entity session grant: Opt-in durable-object preview. Requires the configured
    application allowlist and applicable feature gates; production enablement awaits native GCS/KVM
    acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy
    write access for mutation. Reader grace, claims and unknown acknowledgements must be respected.
    Responses are not cacheable. See the durable entity operator guides for the bounded recovery
    contract.

    Args:
        slug (str):
        body (MintDurableEntitySessionGrantBody): Mint durable entity session grant — requestBody
            / content / application/json / schema: Preview mutation input with server-side size, scope
            and revision validation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MintDurableEntitySessionGrantResponse201 | Problem]
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
    body: MintDurableEntitySessionGrantBody,
) -> MintDurableEntitySessionGrantResponse201 | Problem | None:
    """Mint Durable Entity Session Grant.

     Mint durable entity session grant: Opt-in durable-object preview. Requires the configured
    application allowlist and applicable feature gates; production enablement awaits native GCS/KVM
    acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy
    write access for mutation. Reader grace, claims and unknown acknowledgements must be respected.
    Responses are not cacheable. See the durable entity operator guides for the bounded recovery
    contract.

    Args:
        slug (str):
        body (MintDurableEntitySessionGrantBody): Mint durable entity session grant — requestBody
            / content / application/json / schema: Preview mutation input with server-side size, scope
            and revision validation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MintDurableEntitySessionGrantResponse201 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
