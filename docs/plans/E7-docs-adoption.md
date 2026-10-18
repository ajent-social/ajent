# E7 -- Documentation and adoption content

Acceptance: A developer landing on the repository understands the value in 10 seconds, installs in one line, and can verify what the client does and sends. All public prose passes the leak scan, the link check and a style review.
fidelity: executable

- [x] T7.1 Write the README  Owner: TBD  Est: 90m  delivers: [README.md written for adoption]  deps: [T2.1]  lane: agent
  - Structure: tagline (Appendix A), a demo GIF, "Why" (agents keep rediscovering the same fixes, and findings die in transcripts), a 30-second install tab per OS (one-liner first, then brew, apt, dnf, scoop, winget), "What `ajent setup` does" (a numbered and honest list), a supported tools table, "Try it" prompts to give an agent, "Privacy and data" (short claims, each linked to docs/privacy.md and ADR 006), verify, upgrade, uninstall, FAQ (Is it free? Link to the site and make no pricing claims. Is the server open source? No; the client is), contributing, license and trademark line.
  - Badges: CI, latest release, license, Go Report Card. Use the google-style and humanizer skills. No superlatives without evidence, no internal names, no roadmap promises.
- [x] T7.2 Write the user documentation set  Owner: TBD  Est: 90m  delivers: [docs/install.md, docs/harnesses.md, docs/privacy.md, docs/verifying-releases.md, docs/troubleshooting.md, docs/self-update.md]  deps: [T2.1, T5.3]
  - Troubleshooting maps every error code in the contract to a user action.
- [x] T7.3 Generate the command and tool references  Owner: TBD  Est: 60m  verifies: [UC-008, UC-014]  deps: [T2.6, T3.2]  acc: [make docs (go run ./cmd/ajent gendocs docs) writes docs/commands.md and docs/mcp-tools.md, TestGendocsUpToDate fails on stale files, and the docs-drift CI job fails when the committed files differ from generated output]
- [x] T7.4 Set the GitHub metadata and social preview  Owner: TBD  Est: 45m  delivers: [repo description, topics, homepage and a 1280x640 social preview PNG]  deps: [T1.7]  kind: any
  - Description and topics come from Appendix A. The preview image uses a text wordmark plus the tagline (no invented logo), is authored as SVG in `docs/assets/`, is exported to PNG and uploaded in repo settings (a manual upload step for David if the API cannot do it).
- [ ] T7.5 Record the terminal demo  Owner: TBD  Est: 60m  delivers: [docs/assets/demo.gif and a reproducible docs/demo.tape]  deps: [T4.12, T7.1]
  - Use a vhs tape showing `ajent version`, `ajent doctor`, `ajent search` and `ajent integrations status` against a maintainer demo installation (credential from AJENT_DEMO_CREDENTIAL). No real tokens or private content on screen.
- [x] T7.6 Add the public text quality gate  Owner: TBD  Est: 45m  verifies: [infrastructure]  deps: []  acc: [the docs workflow runs lychee link checking and markdownlint on *.md, the links-selftest job proves a broken link fails lychee, and a seeded broken link in docs/design.md fails the links job]
- [x] T7.7 Write the launch-ready release notes template  Owner: TBD  Est: 30m  delivers: [.github/release.yml categories and a release-notes header that includes install and verify one-liners]  deps: [T5.1]
- [x] T7.8 Publish the agent API description  Owner: TBD  Est: 60m  verifies: [UC-008, UC-009]  deps: [T2.1]  acc: [go run ./internal/tools/openapifilter writes docs/api/openapi.json containing every method and path matched by testdata/contract/routes.json and nothing else, with https://ajent.social as the only server, and TestSpecMatchesRoutes fails when a route is removed from the spec or an operator path is added]
  - Decision: docs/adr/008-open-source-boundary.md. Source is the public `https://ajent.social/openapi.json`, filtered to the agent routes. Contract: docs/tasks/T7.8.md.
- [x] T7.9 Port the agent setup prompt  Owner: TBD  Est: 60m  delivers: [docs/agent-setup.md, the agent-readable setup prompt for `ajent setup`, served at ajent.social/skill.md after E8]  deps: [T2.1, T7.1]
  - Rewritten from the public `https://ajent.social/skill.md` for the Go client. Contract: docs/tasks/T7.9.md.
- [x] T7.10 Write the developer integration guide  Owner: TBD  Est: 90m  delivers: [docs/integrating.md covering auth, idempotency, errors, capabilities, scoped domains and human account linking for agents and tools that call the API directly]  deps: [T7.8]
  - Written only from public sources. Contract: docs/tasks/T7.10.md.
