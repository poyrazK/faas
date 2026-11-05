from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.rotate_org_api_key_request import RotateOrgAPIKeyRequest
from ...models.rotate_org_api_key_response import RotateOrgAPIKeyResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: RotateOrgAPIKeyRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{slug}/keys/{id}/rotate".format(
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
) -> Problem | RotateOrgAPIKeyResponse | None:
    if response.status_code == 200:
        response_200 = RotateOrgAPIKeyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RotateOrgAPIKeyResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateOrgAPIKeyRequest | Unset = UNSET,
) -> Response[Problem | RotateOrgAPIKeyResponse]:
    """Rotate an API key (org-scoped).

     Org-scoped counterpart of `POST /v1/keys/{id}/rotate`. Mints a
    new key (status='active') and demotes the predecessor into the
    grace window in one transaction. The new key inherits the
    predecessor's `org_id` — rotation never silently rebinds across
    orgs. Quota is neutral (-1 +1 = 0).

    Args:
        slug (str):
        id (str):
        body (RotateOrgAPIKeyRequest | Unset): POST /v1/orgs/{slug}/keys/{id}/rotate body. `label`
            overrides the new key's label (inherits from the predecessor when omitted);
            `grace_window_days` is the same per-account override as `PATCH
            /v1/account/keys/grace_window_days` — defaulting to the plan default when omitted
            (`api.DefaultAPIKeyGraceWindowDays = 7`).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RotateOrgAPIKeyResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateOrgAPIKeyRequest | Unset = UNSET,
) -> Problem | RotateOrgAPIKeyResponse | None:
    """Rotate an API key (org-scoped).

     Org-scoped counterpart of `POST /v1/keys/{id}/rotate`. Mints a
    new key (status='active') and demotes the predecessor into the
    grace window in one transaction. The new key inherits the
    predecessor's `org_id` — rotation never silently rebinds across
    orgs. Quota is neutral (-1 +1 = 0).

    Args:
        slug (str):
        id (str):
        body (RotateOrgAPIKeyRequest | Unset): POST /v1/orgs/{slug}/keys/{id}/rotate body. `label`
            overrides the new key's label (inherits from the predecessor when omitted);
            `grace_window_days` is the same per-account override as `PATCH
            /v1/account/keys/grace_window_days` — defaulting to the plan default when omitted
            (`api.DefaultAPIKeyGraceWindowDays = 7`).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RotateOrgAPIKeyResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateOrgAPIKeyRequest | Unset = UNSET,
) -> Response[Problem | RotateOrgAPIKeyResponse]:
    """Rotate an API key (org-scoped).

     Org-scoped counterpart of `POST /v1/keys/{id}/rotate`. Mints a
    new key (status='active') and demotes the predecessor into the
    grace window in one transaction. The new key inherits the
    predecessor's `org_id` — rotation never silently rebinds across
    orgs. Quota is neutral (-1 +1 = 0).

    Args:
        slug (str):
        id (str):
        body (RotateOrgAPIKeyRequest | Unset): POST /v1/orgs/{slug}/keys/{id}/rotate body. `label`
            overrides the new key's label (inherits from the predecessor when omitted);
            `grace_window_days` is the same per-account override as `PATCH
            /v1/account/keys/grace_window_days` — defaulting to the plan default when omitted
            (`api.DefaultAPIKeyGraceWindowDays = 7`).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RotateOrgAPIKeyResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateOrgAPIKeyRequest | Unset = UNSET,
) -> Problem | RotateOrgAPIKeyResponse | None:
    """Rotate an API key (org-scoped).

     Org-scoped counterpart of `POST /v1/keys/{id}/rotate`. Mints a
    new key (status='active') and demotes the predecessor into the
    grace window in one transaction. The new key inherits the
    predecessor's `org_id` — rotation never silently rebinds across
    orgs. Quota is neutral (-1 +1 = 0).

    Args:
        slug (str):
        id (str):
        body (RotateOrgAPIKeyRequest | Unset): POST /v1/orgs/{slug}/keys/{id}/rotate body. `label`
            overrides the new key's label (inherits from the predecessor when omitted);
            `grace_window_days` is the same per-account override as `PATCH
            /v1/account/keys/grace_window_days` — defaulting to the plan default when omitted
            (`api.DefaultAPIKeyGraceWindowDays = 7`).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RotateOrgAPIKeyResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
