from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.custom_domain_response import CustomDomainResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    domain: str,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/domains/{domain}/verify".format(
            domain=quote(str(domain), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CustomDomainResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CustomDomainResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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
) -> Response[CustomDomainResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[CustomDomainResponse | Problem]:
    """Re-verify a domain's DNS + cert (issue

     Re-runs the DNS verifier + cert dial; returns the canonical
    CustomDomainResponse. Used by `gregale domains verify
    <domain>`. Idempotent: POSTing twice does not change the
    durable verification state.

    Args:
        domain (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CustomDomainResponse | Problem]
    """

    kwargs = _get_kwargs(
        domain=domain,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> CustomDomainResponse | Problem | None:
    """Re-verify a domain's DNS + cert (issue

     Re-runs the DNS verifier + cert dial; returns the canonical
    CustomDomainResponse. Used by `gregale domains verify
    <domain>`. Idempotent: POSTing twice does not change the
    durable verification state.

    Args:
        domain (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CustomDomainResponse | Problem
    """

    return sync_detailed(
        domain=domain,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[CustomDomainResponse | Problem]:
    """Re-verify a domain's DNS + cert (issue

     Re-runs the DNS verifier + cert dial; returns the canonical
    CustomDomainResponse. Used by `gregale domains verify
    <domain>`. Idempotent: POSTing twice does not change the
    durable verification state.

    Args:
        domain (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CustomDomainResponse | Problem]
    """

    kwargs = _get_kwargs(
        domain=domain,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> CustomDomainResponse | Problem | None:
    """Re-verify a domain's DNS + cert (issue

     Re-runs the DNS verifier + cert dial; returns the canonical
    CustomDomainResponse. Used by `gregale domains verify
    <domain>`. Idempotent: POSTing twice does not change the
    durable verification state.

    Args:
        domain (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CustomDomainResponse | Problem
    """

    return (
        await asyncio_detailed(
            domain=domain,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
