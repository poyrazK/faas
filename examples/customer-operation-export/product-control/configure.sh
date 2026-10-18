#!/usr/bin/env bash
# Publishes reviewed internal product terms and assigns three existing customers.
# Run using an owner profile after qualifying the exporter and its Auth app.
set -euo pipefail
: "${PRODUCT_APP:?Set the existing export app slug}"
: "${PRODUCT_AUTH_APP:?Set the account-owned Application Auth app slug}"
: "${PRODUCT_ALICE_TENANT:?Set Alice's platform tenant UUID}"
: "${PRODUCT_BOB_TENANT:?Set Bob's platform tenant UUID}"
: "${PRODUCT_CAROL_TENANT:?Set Carol's platform tenant UUID}"
: "${PRODUCT_ALICE_ORG:?Set Alice's Application Auth organization UUID}"
: "${PRODUCT_BOB_ORG:?Set Bob's Application Auth organization UUID}"
: "${PRODUCT_CAROL_ORG:?Set Carol's Application Auth organization UUID}"
product_pilot_cli="${GREGALE_BIN:-gregale}"
product_pilot_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
product_pilot_tmp="$(mktemp -d)"
trap 'rm -rf -- "$product_pilot_tmp"' EXIT
python3 - "$product_pilot_dir/product.json" "$product_pilot_tmp/product.json" <<'PY'
import json, os, sys
with open(sys.argv[1], encoding='utf-8') as source:
    catalog = json.load(source)
catalog['auth_app'] = os.environ['PRODUCT_AUTH_APP']
with open(sys.argv[2], 'x', encoding='utf-8') as destination:
    json.dump(catalog, destination, indent=2)
    destination.write('\n')
PY
"$product_pilot_cli" product validate --file "$product_pilot_tmp/product.json"
"$product_pilot_cli" product apply --app "$PRODUCT_APP" --file "$product_pilot_tmp/product.json"
"$product_pilot_cli" product customers assign --app "$PRODUCT_APP" --tenant "$PRODUCT_ALICE_TENANT" --auth-org "$PRODUCT_ALICE_ORG" --plan starter
"$product_pilot_cli" product customers assign --app "$PRODUCT_APP" --tenant "$PRODUCT_BOB_TENANT" --auth-org "$PRODUCT_BOB_ORG" --plan pro
"$product_pilot_cli" product customers assign --app "$PRODUCT_APP" --tenant "$PRODUCT_CAROL_TENANT" --auth-org "$PRODUCT_CAROL_ORG" --plan starter
"$product_pilot_cli" product customers list --app "$PRODUCT_APP"
