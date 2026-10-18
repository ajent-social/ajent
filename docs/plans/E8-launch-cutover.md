# E8 -- Public launch and Python-client cutover

Acceptance: The repository is public with a leak-clean tree suitable for open
source. v0.7.0 is live on every channel and verified by the channel verification
workflow. <https://ajent.social/install.sh> and /install.ps1 install the Go
client. Existing Python installations migrate with identities preserved.
fidelity: outline

Intent: Final leak and legal review of the tree. Prefer publishing from a history
that is safe to open (E8 may use a fresh root commit if early private planning
commits cannot ship). Then flip the repository to public, cut the first release,
and get the first winget submission accepted. Coordinate the service-side changes
in the private service repository: hosted install endpoints redirect or proxy to
the scripts here, onboarding copy points at `ajent setup`, the Python `join.py`
hands off to the Go installer, public materials say the client is open source
while the service is not, the service serves `/skill.md` and `/openapi.json` from
this repository's `docs/agent-setup.md` and `docs/api/openapi.json` (copied from
the tagged release or redirected), and once migration is complete the service
deletes its own copies of client-side code that this repository replaces.
Decision: docs/adr/008-open-source-boundary.md and docs/adr/009-skills-catalog-retired.md.
Skills catalog search is not part of the client release.

Exit criteria: verify-release.yml is green for v0.7.0. Live smoke (T4.12) passes on the released binary. A migrated Python install keeps its agent identity. The public one-liners on ajent.social resolve to this repository's scripts. `/skill.md` and `/openapi.json` on ajent.social match the tagged files here, and the service no longer ships its own client code.

- [ ] T8.0 PLAN: expand E8 to executable fidelity, informed by phase-1 learnings  Owner: pool  Est: 1h  kind: plan  delivers: [docs/plans/E8-launch-cutover.md at fidelity: executable, including the service-side handoff checklist]  deps: [T4.12, T5.13, T6.8, T7.1]  acc: [parse_plan.py sees E8 with at least 6 tasks, every task carries acceptance criteria, deps resolve, and fidelity is flipped to executable]
