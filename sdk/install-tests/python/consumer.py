"""Executed with -I in a fresh virtual environment containing the built wheel."""
import sys
from importlib.resources import files

from faas_sdk import ErrNotFound, FaaSClient
from faas_sdk.api.account import get_account
from faas_sdk.api.apps import get_app
from faas_sdk.models.account_response import AccountResponse
from faas_sdk.models.app_response import AppResponse
from faas_sdk.workflows import workflow

client = FaaSClient(base_url=sys.argv[1], token="sdk-install-fixture")
try:
    account = get_account.sync(client=client.inner)
    assert isinstance(account, AccountResponse)
    assert account.plan == "hobby"
    app = get_app.sync("hello-world", client=client.inner)
    assert isinstance(app, AppResponse)
    assert app.slug == "hello-world"
    assert callable(workflow)
    assert files("faas_sdk").joinpath("py.typed").is_file()
    try:
        get_app.sync("missing-app-404", client=client.inner)
    except ErrNotFound as error:
        assert error.problem.code == "not_found"
        assert error.status == 404
        assert error.tx_id
    else:
        raise AssertionError("expected the installed SDK's ErrNotFound")
    print("Python installed SDK: account, typed app, workflow, typing marker and typed 404 verified")
finally:
    client.close()
