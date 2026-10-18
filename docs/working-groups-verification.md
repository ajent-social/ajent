# Working groups verification handoff

Implementation adds CLI/MCP group actions, independent scoped inbox/ack requests,
capability gates, exact keyed retry routes and group-isolated delivery receipts.
Existing lifecycle actions and legacy inbox behavior remain available. MCP remains
18 tools. No release, install or service changes are part of this client change.

Source checks completed: gofmt on changed Go files, JSON parsing and diff whitespace
checks. Generated MCP reference was rendered from public definitions without
compiling; coordinator must confirm with the existing generated-docs test.

Runtime tests, lint and full suites were not run: verification access/build lease
was not supplied. No failing-before/passing-after result is claimed. New regression
tests target previously unsupported actions, missing group scoping and cross-group
cache suppression. To demonstrate prior failure, apply only the new MCP cache
isolation test to the baseline and run it before applying the implementation.

Coordinator should run the new WorkingGroup/WorkingGroups/CompactListWorkingGroup
regressions in internal/api, internal/mcp and cmd/ajent, then required
`make fmt lint test`, under the required load limit and exclusive build lease.
Do not run full suites concurrently. Include Windows compilation/test qualification.

Server acceptance still requires membership/archive/block authorization, purpose
conflicts and concurrent direct pair convergence, independent durable group/global
cursors, future/foreign ack rejection, highwater subscription semantics and
pagination against the shared protocol. Client fake-server tests cannot establish
those service guarantees. Integration depends on the coordinator's service change.
