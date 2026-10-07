from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.global_runtime_consent_response import GlobalRuntimeConsentResponse
from ...models.problem import Problem
from ...models.put_global_runtime_consent_request import PutGlobalRuntimeConsentRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    consent_id: UUID,
    *,
    body: PutGlobalRuntimeConsentRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/global-apps/{slug}/runtime-consents/{consent_id}".format(
            slug=quote(str(slug), safe=""),
            consent_id=quote(str(consent_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    _kwargs["follow_redirects"] = False
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GlobalRuntimeConsentResponse | Problem | None:
    if response.status_code == 200:
        response_200 = GlobalRuntimeConsentResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

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
) -> Response[GlobalRuntimeConsentResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    consent_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalRuntimeConsentRequest,
) -> Response[GlobalRuntimeConsentResponse | Problem]:
    """Register original regional runtime consent.

     Requires verified directory personal owner write access, MFA when enabled, and a request-only
    regional account-admin key. Active consent lasts at most six days and is bounded to 32 records per
    app. UUID and input retries retain the original authority. Redirects must never be followed;
    regional credentials are never retained. Request bodies are bounded to 4096 bytes.

    Args:
        slug (str):
        consent_id (UUID):
        body (PutGlobalRuntimeConsentRequest): One request-only owner key and expiry for an
            original regional runtime consent.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalRuntimeConsentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consent_id=consent_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    consent_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalRuntimeConsentRequest,
) -> GlobalRuntimeConsentResponse | Problem | None:
    """Register original regional runtime consent.

     Requires verified directory personal owner write access, MFA when enabled, and a request-only
    regional account-admin key. Active consent lasts at most six days and is bounded to 32 records per
    app. UUID and input retries retain the original authority. Redirects must never be followed;
    regional credentials are never retained. Request bodies are bounded to 4096 bytes.

    Args:
        slug (str):
        consent_id (UUID):
        body (PutGlobalRuntimeConsentRequest): One request-only owner key and expiry for an
            original regional runtime consent.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalRuntimeConsentResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        consent_id=consent_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    consent_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalRuntimeConsentRequest,
) -> Response[GlobalRuntimeConsentResponse | Problem]:
    """Register original regional runtime consent.

     Requires verified directory personal owner write access, MFA when enabled, and a request-only
    regional account-admin key. Active consent lasts at most six days and is bounded to 32 records per
    app. UUID and input retries retain the original authority. Redirects must never be followed;
    regional credentials are never retained. Request bodies are bounded to 4096 bytes.

    Args:
        slug (str):
        consent_id (UUID):
        body (PutGlobalRuntimeConsentRequest): One request-only owner key and expiry for an
            original regional runtime consent.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalRuntimeConsentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consent_id=consent_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    consent_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalRuntimeConsentRequest,
) -> GlobalRuntimeConsentResponse | Problem | None:
    """Register original regional runtime consent.

     Requires verified directory personal owner write access, MFA when enabled, and a request-only
    regional account-admin key. Active consent lasts at most six days and is bounded to 32 records per
    app. UUID and input retries retain the original authority. Redirects must never be followed;
    regional credentials are never retained. Request bodies are bounded to 4096 bytes.

    Args:
        slug (str):
        consent_id (UUID):
        body (PutGlobalRuntimeConsentRequest): One request-only owner key and expiry for an
            original regional runtime consent.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalRuntimeConsentResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            consent_id=consent_id,
            client=client,
            body=body,
        )
    ).parsed
