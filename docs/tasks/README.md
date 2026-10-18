# Task contracts

Each file `docs/tasks/<task-id>.md` is the complete brief for one row of docs/plan.md. It is written so that a headless coding agent with no prior context can execute the task unattended. The row in `docs/plans/E*.md` and its contract are one artifact: change them together, and delete the contract when the row ships.

Required sections, in order (checked by `scripts/check-task-contract.sh`):

    ## <task-id>: <title>
    ### Objective
    ### Read first
    ### Scope
    ### Out of scope
    ### Steps
    ### Acceptance criteria
    ### Verification
    ### Pointers

Rules for contract authors:

- Scope lists every file the task may create or modify, as exact paths. The agent must not touch anything else.
- Steps are numbered and prescriptive: exact identifiers, flag names, JSON field names, file paths and test names. Nothing is left to judgment that the plan could decide.
- Acceptance criteria include a genuine-red check for each behavior change: break the behavior, observe the named test fail, restore it, and report both outputs.
- Verification lists the exact commands, scoped to the changed packages.
- Signatures come from docs/interfaces.md. Behavior comes from docs/contract.md. A contract never contradicts either.
- Never include branch names, open-PR lists, commit SHAs or process rules (those live in AGENTS.md).
- A contract is certified when the cheapest intended model tier executes it to green once. Record that as a final line: `Certified: <model-id> <date> <PR#>`.
