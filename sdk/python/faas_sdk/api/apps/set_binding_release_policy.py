from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.binding_release_policy import BindingReleasePolicy
from ...models.problem import Problem
from ...models.set_binding_release_policy_request import SetBindingReleasePolicyRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: SetBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/bindings/release-policy".format(
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
) -> BindingReleasePolicy | Problem | None:
    if response.status_code == 200:
        response_200 = BindingReleasePolicy.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BindingReleasePolicy | Problem]:
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
    body: SetBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> Response[BindingReleasePolicy | Problem]:
    """Replace a binding release policy using its current revision.

     Requires deploy:write or admin and completed MFA. expected_revision is
    mandatory (0 initially); each accepted write increments it. Enforcement
    requires complete fresh verification on every deployment gaining traffic,
    including through redistribution. Promotion, direct traffic PATCH and
    canary advance evaluate the policy; request flags may only strengthen it.
    New candidates must be admitted with explicit zero traffic. Automatic
    cutovers, legacy recovery and project release graph switches fail closed
    until they support binding fences. To recover without evidence, explicitly
    set off with the current revision and a reason, then retry. Every update
    has durable policy history. Existing traffic is not changed by this call.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetBindingReleasePolicyRequest): Compare-and-set binding release enforcement for one
            deployment scope, including evidence age, application acknowledgements and an explicit
            change reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BindingReleasePolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> BindingReleasePolicy | Problem | None:
    """Replace a binding release policy using its current revision.

     Requires deploy:write or admin and completed MFA. expected_revision is
    mandatory (0 initially); each accepted write increments it. Enforcement
    requires complete fresh verification on every deployment gaining traffic,
    including through redistribution. Promotion, direct traffic PATCH and
    canary advance evaluate the policy; request flags may only strengthen it.
    New candidates must be admitted with explicit zero traffic. Automatic
    cutovers, legacy recovery and project release graph switches fail closed
    until they support binding fences. To recover without evidence, explicitly
    set off with the current revision and a reason, then retry. Every update
    has durable policy history. Existing traffic is not changed by this call.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetBindingReleasePolicyRequest): Compare-and-set binding release enforcement for one
            deployment scope, including evidence age, application acknowledgements and an explicit
            change reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BindingReleasePolicy | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> Response[BindingReleasePolicy | Problem]:
    """Replace a binding release policy using its current revision.

     Requires deploy:write or admin and completed MFA. expected_revision is
    mandatory (0 initially); each accepted write increments it. Enforcement
    requires complete fresh verification on every deployment gaining traffic,
    including through redistribution. Promotion, direct traffic PATCH and
    canary advance evaluate the policy; request flags may only strengthen it.
    New candidates must be admitted with explicit zero traffic. Automatic
    cutovers, legacy recovery and project release graph switches fail closed
    until they support binding fences. To recover without evidence, explicitly
    set off with the current revision and a reason, then retry. Every update
    has durable policy history. Existing traffic is not changed by this call.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetBindingReleasePolicyRequest): Compare-and-set binding release enforcement for one
            deployment scope, including evidence age, application acknowledgements and an explicit
            change reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BindingReleasePolicy | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetBindingReleasePolicyRequest,
    scope: str | Unset = "default",
) -> BindingReleasePolicy | Problem | None:
    """Replace a binding release policy using its current revision.

     Requires deploy:write or admin and completed MFA. expected_revision is
    mandatory (0 initially); each accepted write increments it. Enforcement
    requires complete fresh verification on every deployment gaining traffic,
    including through redistribution. Promotion, direct traffic PATCH and
    canary advance evaluate the policy; request flags may only strengthen it.
    New candidates must be admitted with explicit zero traffic. Automatic
    cutovers, legacy recovery and project release graph switches fail closed
    until they support binding fences. To recover without evidence, explicitly
    set off with the current revision and a reason, then retry. Every update
    has durable policy history. Existing traffic is not changed by this call.

    Args:
        slug (str):
        scope (str | Unset):  Default: 'default'.
        body (SetBindingReleasePolicyRequest): Compare-and-set binding release enforcement for one
            deployment scope, including evidence age, application acknowledgements and an explicit
            change reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BindingReleasePolicy | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            scope=scope,
        )
    ).parsed
