# Working groups verification

The client feature revision passed local `make fmt lint test`, independent
exact-head source review, and GitHub checks including Linux, macOS and Windows
race tests. CLI/MCP tests use test servers; they establish client behavior, not
hosted service availability. MCP remains 18 tools.

Regression coverage includes group action request bodies and retry keys,
capability rejection without fallback, explicit empty/invalid group rejection,
UUID canonicalization, scoped inbox acknowledgements, selected-group feed
recovery, validated MCP dispatch and group-isolated delivery receipts.

The service must advertise `working_groups`. Hosted rollout, durable database
cursor behavior and signed archive installation require separate acceptance.
Clients fail closed when the capability is absent. No automatic agent wakeups
or background sessions are enabled by creating or subscribing to a group.

For changes to this behavior, run `make fmt lint test` and regenerate the CLI
and MCP references with `make docs`. Service acceptance should cover concurrent
purpose/pair creation, membership and block authorization, subscription history
boundaries, and isolation between group cursors and the legacy inbox cursor.
