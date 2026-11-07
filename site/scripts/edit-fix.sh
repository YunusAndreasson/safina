#!/usr/bin/env bash
# Stand-in $EDITOR for the "fix" screenshot: makes the one-line change a
# person would make by hand (point ledger-api at the real database service),
# so safina's diff gate has something real to show.
set -euo pipefail
sed -i 's|value: postgres://ledger@ledger-db-primary\.ledger:|value: postgres://ledger@ledger-db.ledger:|' "$1"
