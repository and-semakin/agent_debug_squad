## Context

See proposal.md for motivation. Initial discovery used worktree commit `7d21ef5`. This revision reconciles the change against local `main` commit `1c444a2` (2026-09-27), which still uses version-1 definitions/schemas 1–3 but also specifies backend installation/readiness preflight, managed runtimes, one-shot execution, and the current threshold precedence. Explicit workflow threshold wins over startup-loaded machine judge threshold, then the built-in 0.7; settled evidence and omitted-field identity stay unchanged. Those shipped contracts are preserved. Implementation started from `origin/main` at `2f09a36`, with no intervening changes to the affected main specs.

Observed implementation at initial discovery (loop architecture remains applicable; integrate with the newer preflight lifecycle before implementation):

- `internal/config/workflow.go` parses `until_task`/`on_verdict`, checks raw cycles, parent forests, unique sinks, and cycles in recursively projected scope graphs. `projectToScope` already collapses immediate child subtrees but this is currently a validation abstraction.
- `internal/domain/workflow_loops.go` provides ancestry, subtree and complete-path helpers. `internal/workflow/loops.go` consults a condition only after acceptable whole-iteration settlement. `loops_nested.go` handles recursive reset and descendant final-result lookup.
- `schedule.go` checks direct task dependencies and outgoing loop barriers. A child task with no direct incoming dependency can start independently: a projected edge into its sibling does not currently gate the entire child. `lastCommittedAttemptUnderPrefix` can select a prior inner iteration when a final producer never ran. Both need deliberate changes.
- `loop_controls.go` already prepares copied snapshots, saves them atomically, and adopts them only after persistence. Other scheduling/judge paths mutate live state more directly. Control resolution must use the stronger transaction pattern.
- `internal/store/workflow.go` has an authoritative atomically replaced `workflow.json`, duplicate-key/path validation, readable attempt artifacts, and an auxiliary event log. There is no transactional database.
- Existing tests in `conditions_test.go`, `loops_nested_test.go`, `loop_recovery_test.go`, `controls_test.go`, and store/config nested tests supply useful fixtures, but their terminal-condition expectations must change explicitly.

## Goals / Non-Goals

**Goals:** One local control primitive that safely partitions each invocation into phases; testable admission, settlement, skip, replay and recovery semantics; explicit missing-result behavior and version boundaries.

**Non-Goals:** Arbitrary branching/jumps, target-loop labels, cancellation of concurrent side effects, new agent lifecycles, new result aggregation/export mechanisms, cross-iteration expression languages, workspace isolation, or automatic retries. An independent root loop or manual agent may still run concurrently: barriers cover the owning subtree, not shared filesystem effects across the whole squad.

## Decisions

### 1. Syntax and version boundary

Use `workflow.version: 2` for all new loop workflows. A task keeps `agent`, `prompt`, `needs`, `loop`, and `verdicts`, and optionally adds `control`, a direct verdict-to-action map. The owner is always its existing `loop`; there is no target field. Omission means an ordinary task. A present map must be nonempty, total over declared verdicts, contain no additional keys, and contain only `proceed`, `continue`, `break`, `needs_attention`. Controls require at least two verdicts and `allowed_to_fail: false` (or omission). Synthetic `uncertain` cannot be mapped.

A loop has positive `max_iterations`, optional `parent`, and optional `on_exhaustion: needs_attention|succeed`. Exhaustion is configurable only when that loop directly owns at least one control. A container-only loop is fixed-count; a child control never makes its ancestor controlled. No-control loops finish at their effective cap. All-control maps containing only proceed remain controlled and therefore default to attention at the cap; authors can explicitly choose succeed.

New version-1 loop definitions and all occurrences of `until_task`/`on_verdict` are rejected with task/loop-specific migration diagnostics, not silently normalized. Loopless version 1 stays supported with unchanged identity/serialization. Version 2 is also valid without loops, and always uses schema 4. Version, control maps, ownership, needs, policies and caps participate in canonical definition identity. Version-1 idempotency records are never matched by version-2 submissions.

Example workflow fragment (agent names refer to distinct configured agents):

```yaml
workflow:
  version: 2
  name: review-mr-queue
  max_parallel: 2
  loops:
    queue:
      max_iterations: 100
      on_exhaustion: needs_attention
  tasks:
    refresh_queue:
      agent: queue_reader
      loop: queue
      prompt: Refresh the current MR queue and save its current contents.
    select_mr:
      agent: selector
      loop: queue
      needs: [refresh_queue]
      prompt: Select the next MR, or report that the queue is empty.
      verdicts:
        mr_found: An MR is selected in this response.
        no_mrs_left: The refreshed queue is empty.
      control:
        mr_found: proceed
        no_mrs_left: break
    review_a:
      agent: reviewer_a
      loop: queue
      needs: [select_mr]
      prompt: Review the selected MR.
    review_b:
      agent: reviewer_b
      loop: queue
      needs: [select_mr]
      prompt: Independently review the selected MR.
    consolidate:
      agent: consolidator
      loop: queue
      needs: [review_a, review_b]
      prompt: Consolidate both reviews and update queue processing status.
      verdicts:
        review_complete: Processing this MR is complete.
        needs_human: A human decision is required.
      control:
        review_complete: proceed
        needs_human: needs_attention
    report:
      agent: reporter
      needs: [select_mr]
      prompt: Report the terminal queue selection outcome; do not assume it contains a review.
```

The report deliberately depends on a producer that executes even on an empty queue. It is not an all-MR aggregate. A report requiring `consolidate` on the final empty-queue pass is blocked by its missing result. Aggregate exports or optional dependency syntax would be separate features. External queue updates are application-level effects; caps prevent unbounded polling but do not ensure queue mutation or successful review.

Why this spelling: `control` signals a scheduling effect and avoids repeating `on_verdict` beside `verdicts`. `proceed` means finish this iteration, `continue` means next iteration, matching programming-language loop terminology. These are configuration actions, not new HTTP commands. `next` and `repeat` were considered but offer little benefit and leave the proceed/next distinction just as necessary.

### 2. Barrier validation: comparability at each scope

Let edges point from dependency to consumer. First run existing structural validation: valid identifiers, strict fields/types, declared agents/tasks/loops, unique agent per task, parent forest, no unrelated-branch direct dependencies, raw DAG and recursively projected DAG checks.

For each scope S (workflow scope and each loop), construct Q(S):

1. Vertices are directly owned tasks and one vertex for each immediate child loop with its entire subtree.
2. For each raw edge with both endpoints in S's subtree, project each endpoint to its direct task or containing immediate child. Keep distinct-endpoint edges; discard internal self-projections here and validate them recursively below.
3. Reject a cycle. Edges leaving S are handled by enclosing scopes and never establish a path for a local barrier.
4. For each control c directly owned by S (workflow scope controls are invalid), compute strict ancestors A(c) by reverse reachability and strict descendants D(c) by forward reachability **inside Q(S)**.
5. Require A(c) union {c} union D(c) to equal all vertices of Q(S). Any remaining vertex is incomparable and invalid. Name c, S, and the first lexicographic incomparable task/child loop, with a `needs` correction example. Child vertices use typed identities, not ambiguous task-name strings.

This admits a head, middle, tail, or singleton control, plus parallel fan-out/fan-in within phases. Any two controls must be comparable, so controls have a unique dependency order; a total topological ordering of ordinary tasks is not required. A simple pair of traversals per control costs O(C*(V+E)) per scope after projection. This is adequate for current workflow sizes; cached reachability is an optimization, not a requirement.

Counterexamples and accepted forms:

| Graph within owner | Result |
| --- | --- |
| `c -> a`, independent `b` | Reject: b could act concurrently with c. |
| `a -> c -> b`, `a -> x -> b` | Reject: x bypasses c even though the graph has one source and one sink. |
| `a,b -> c -> d,e -> c2 -> f` | Accept: both controls partition all work. |
| independent `c1` and `c2` joining at `z` | Reject: conflicting concurrent decisions have no order. |
| `c -> inner.a`, independent `inner.b` | Accept structurally as `c -> inner`, **only with subtree admission below**. |
| `inner.a -> c`, independent `inner.b` | Accept as `inner -> c`; c waits for all of inner, including b and all its iterations. |
| `inner.a -> c -> inner.b` | Reject projected `inner -> c -> inner` cycle, even if the raw graph is acyclic. |
| inner control and independent directly owned outer task | Accept if outer has no conflicting outer control; inner decisions affect only inner. |

Comparability is sufficient only together with the runtime contracts below. `max_parallel: 1`, lexical scheduling, early quorum, or cancellation cannot substitute for it.

Planning check: an exhaustive small-graph model enumerated every forward-edge DAG on one through five topologically labelled vertices and each possible control (5,405 graph/control cases). It compared the reachability criterion against all dependency-closed completed sets and found no counterexample: accepted controls never shared a ready frontier with another vertex. This checks the abstract DAG criterion only, not Go implementation, live-worker cleanup or persistence. The child-entry/exit counterexamples above establish why the additional runtime admission contract is still necessary.

### 3. Runtime phase barriers and nested admission

Reuse Q(S) as a scheduling plan, not just a validator. A child loop is admitted as one scope vertex: **no descendant initial attempt or retry may be reserved until all incoming projected predecessor vertices settle acceptably**, including all preceding controls with committed release decisions. A predecessor child is complete only after its entire invocation finishes. Projected edges enforce ordering/acceptance, not data fan-out: each task still receives only its declared dependency inputs plus documented historical carry-over.

Each directly owned control waits for all Q(S) ancestors to settle acceptably; ancestor child vertices must finish their entire invocations. Success thresholds and tolerable errors use existing task rules, and a blocked or interrupted phase cannot be bypassed. No vertex in D(c), including any descendant of a child vertex there, may be reserved before c's decision commits. Apply every enclosing scope's admission and phase gates recursively. A child cannot be admitted based on just one internal task becoming ready. Ordinary same-phase tasks can still run concurrently up to the unchanged global limit.

Proof sketch: for c, every owner vertex lies before c or after c. Completed predecessor child vertices have no live descendants; every descendant child vertex is unadmitted. Thus no worker, queued retry, or unresolved judging attempt in c's owner subtree other than c's judging phase can overlap c's decision. Preceding controls have committed proceed releases. Descendant work has no external effect to undo. Induction on nested scopes gives the same property at every depth. Independent work outside that subtree is allowed; an attention cause still gates all future dispatch globally as today.

At decision time assert this invariant against saved/live state. A violation is corruption/invariant failure and holds execution; never cancel unexpected live workers to manufacture a valid break. Pending suffix tasks are not evaluated as failure holds before their phase opens; otherwise a skipped phase could create a false global hold.

### 4. Actions, counters, and precedence

| Event | Current suffix | Owner transition |
| --- | --- | --- |
| `proceed` | Release after durable decision and global gates | Stay in this iteration; natural completion repeats. |
| `continue` | Persist control skips | Close iteration; advance if budget permits, else exhaustion. |
| `break` | Persist control skips | Close iteration and complete owner, even at cap. |
| `needs_attention` | Keep pending behind barrier | Live control hold; no skips or advance yet. |
| Body naturally completes | All work settled acceptably | Stop if requested, else advance below cap; at cap complete a fixed loop or apply controlled exhaustion. |

`max_iterations` counts entered passes, not reviews, judge calls, or useful work. Root pass 1 is entered at submission; a child pass 1 is entered on admission. Each committed local advance consumes one more pass. A head break uses one pass, executes the selector once and no useful suffix; zero passes are not promised. A head continue also consumes a pass, even if every remaining task is skipped. Multiple checks within one pass do not consume extra passes. At cap `proceed` still permits the current suffix; natural acceptable completion is itself an implicit request to repeat, so it invokes controlled exhaustion at the cap just as continue does. `needs_attention` always holds at the cap. Default exhaustion for controlled loops is attention; explicit succeed completes without claiming all work succeeded. A skip on continue at the cap is retained during exhaustion and after extension: extension starts a new pass, it does not run the skipped suffix.

Stop requested before an automatic advance wins; if advance committed first the stop applies to the new context. Arithmetic for declared caps, positive extensions and counter increments must reject overflow; no wrapping into an effectively unlimited loop. Automatic task-attempt bounds remain the product of finite effective caps along the owner chain; use the maximum cap across historical invocations for the conservative bound. Skips reduce dispatch count. Retries and judge reclassification never increment counters; only explicit extensions raise budgets. No automatic unlimited mode, backoff, wall-clock limit, or retry-count limit is added.

`break` and `continue` target only the direct owner. A child break produces a completed child invocation for its parent; a fixed parent can still repeat. To break the parent, add a parent-owned control consuming the completed child's result. An outer break skips any not-admitted child vertices after it, never already-completed child results. An outer continue resets children only on the subsequent committed parent advance.

### 5. Skips, history and handoff

Add a `skipped` **task outcome**, not a fabricated backend attempt. A skip record has task ID, complete planned `iteration_path`, reason `loop_break` or `loop_continue`, decision ID and controlling owner path/task/attempt. It has no run ID, result, verdict, or attempt number. Attempt numbering remains monotonic over real attempts/retries. All suffix vertices receive outcomes: direct tasks get skips; unadmitted child invocations are recorded `skipped` with `entered: false`, and their subtree tasks get skips at planned local counter 1 under the current prefix. This applies recursively to invocation records too: skipping child and grandchild under outer=2 records paths outer=2/child=1 and outer=2/child=1/grandchild=1, each with entered false and iterations_started zero. No hypothetical child iterations 2..N are created or counted. Schema 4 records loop admission (`entered`) separately from planned path, so an unentered child is never reported as having consumed a pass. Current-view counters are planned counters until admission; `iterations_started` exposes the consumed count (zero for unentered children).

An atomic decision contains all affected task/child identities. Persist closed iteration/invocation summaries and the skip ledger in the authoritative snapshot, not just a best-effort event file. This makes skips from a continue visible after resetting current task views and makes historical final paths explicit. Already-completed outcomes and response files remain untouched. Controlled skips are acceptable for **closing their owner's iteration** and do not degrade the workflow's final outcome by themselves. They are not successful dependencies.

Dependency resolution follows recorded final paths recursively, never "latest successful/committed under prefix":

1. Resolve the consumer's exact scope, wait for intervening child/root completion barriers, then follow each child's recorded final iteration in that enclosing context.
2. Select the producer's latest attempt at that exact path, or its explicit skip record. A queued retry is pending, not an older outcome.
3. A selected skip is an unavailable dependency: block with `dependency_skipped:<producer>:<decision-id>`. Neither producer `allowed_to_fail` nor consumer success threshold waives it. A mandatory absent outcome with no skip is a consistency error and attention, not permission to read history.

An outer consumer blocked by an inner skip holds its own unfinished loop under existing blocking rules. A workflow-scope consumer with a skipped dependency becomes terminal blocked/failure once normal finalization permits. There is no optional-result default or retry of skipped work; correct the workflow for a new execution or cancel a held one. A consumer of a completed control or another prefix producer still receives that actual current result after loop completion. Outcome manifests/views expose skips without fake attempt/run identities.

Retain existing `previous_iteration` and `ancestor_previous_iterations` as **explicitly labelled historical context**, including skips and exact summarized paths. Summarize only the immediately previous enclosing pass and each nested invocation's final path. If pass 2 skipped review, pass 3 history says skipped; it does not silently reach back to pass 1. This is the existing contract for feedback, not satisfaction of `needs`. Referenced successful artifacts remain verified and complete. An explicit `inputs.previous` DSL is unnecessary for this feature: selectors can refresh the external queue and existing carry-over supports review feedback. Selective historical inputs or stable loop exports would be separately scoped if demanded later.

### 6. Durability and recovery

Use schema 4 for all version-2 executions, flat or nested. All loop-owned records carry complete paths; workflow-scope records do not. Add typed task outcomes, invocation/iteration summaries (entered count, final path, close reason), and a decision ledger. A decision key is execution + owner path + task + attempt + outcome revision. Its public ID matches `lcd_[1-9][0-9]*`: an execution-local positive decimal sequence atomically allocated with the decision, persisted, stable across replay/restart, and never reused after commit. It is opaque and contains no colon, slash or equals, so it occupies one reason field. Lookups use execution ID plus decision ID to recover the full causal context. Overflow holds rather than reuses an ID. Each manual outcome revision gets a new ID; stop resolution keeps the held decision ID and appends its evidence. Its record stores verdict/source, mapped action, effective action (for stop handling), affected skips, status (held/released/closed), and any resolving control request. Historical judge evidence stays unchanged; overrides append a new outcome revision.

Under the manager's serialization, prepare a candidate snapshot. A settled control verdict and its decision/hold, skip set, owner close/exhaustion state, and manual request replay record commit in **one authoritative replacement**. An already-held decision can transition once through an override/stop. No observer sees succeeded control with suffix ready before the decision. Only adopt the candidate and notify after successful save. Raw response/decision artifacts may be written first, but orphan files are not authoritative. Event-log appends are audit mirrors; failure there must not replay an applied decision.

An advance can be a second transaction: it consumes a persisted closed-iteration marker once, increments the counter and resets all subtree current state together. Between close and advance there is no eligible suffix. Persist the next context before any attempt reservation. Duplicate callbacks, lost wakeups and repeated reconciliation consult the decision ledger and cannot skip/advance twice. Storage failure freezes dispatch; no candidate state or replay entry leaks into the live snapshot.

Recovery validates definition version/schema pairing, graph/barrier invariants, full ancestry paths, exactly one current record per declared loop, closed versus open contexts, skip ownership and complete suffix sets, no attempts in control-skipped contexts, no repeated decision application, and consistent counts/budgets. Historical ledger paths must identify real entered passes or explicitly unentered skipped children. Invalid state fails closed. Unknown schema versions remain unsupported. Reserved/running backend work is interrupted and needs confirmed explicit retry; judging with a committed response can be reclassified. A committed action is never reclassified or remapped. Crash before decision commit leaves the last judging/held state; after commit it preserves action and skips. This guarantees durable scheduling decisions, not exactly-once external side effects.

### 7. Intervention and retries

Existing HTTP routes stay. Semantics for schema 4:

- **Pause:** persist no-new-dispatch mode. Running responses and control decisions may settle, including skips/owner completion. Do not advance a loop or dispatch suffix/child work until resumed. Unrelated attention also prevents automatic advance/dispatch, but not safe local decision recording/completion.
- **Resume:** revalidate artifacts/reclassify eligible judging attempts, then honor all remaining holds. Resume alone cannot reinterpret a mapped attention verdict as proceed, undo skips, or reset the budget.
- **Override:** only an unresolved judging attempt or the current live mapped attention decision may receive a declared verdict. Re-evaluate the map atomically. A released/closed decision is irreversible regardless of mapped action, even while paused or before suffix dispatch. This explicitly includes a mapped needs_attention decision released by stop as effective proceed; a new override returns 409. Overrides preserve previous verdict evidence and are idempotent before checking current path/terminal state. No direct action override endpoint.
- **Retry:** failed/interrupted attempts only, with existing expected-attempt, cleanup, replay and consumption guards. Additionally reject retry that would invalidate any committed control decision in the relevant scope/path, including the effective projected ancestors of that control and consumed child outcomes. A held attention decision also fences its prefix; resolve it by override or stop. Skipped tasks have no retryable attempt. A failed control before decision is normally retryable in place. Preserve same-path reopening of a fixed loop or a completed child only when no enclosing decision/consumer has consumed it; do not reopen a controlled invocation past its committed close decision.
- **Stop:** preserve graceful finish-current-pass intent and propagation to unfinished descendants. Do not dispatch or skip immediately merely because stop was requested. At a resolved control, break still skips/completes; continue still skips but closes without another pass. Proceed releases its suffix normally. A mapped attention hold becomes an audited **effective proceed** under stop so its suffix can finish; preserve the original verdict/mapping and record why the hold cleared. Unresolved judging, mandatory failures, interruptions and unmet thresholds still require repair. Natural completion and exhausted closed iterations finish under stop. Stop while paused records intent but does not run the suffix. A not-yet-admitted child with propagated stop still runs its first pass if its phase is later reached, unless an actual break/continue skips it. Done/skipped descendants are preserved.
- **Extend:** positive finite cap increase scoped to the current invocation; clears only exhaustion, never control attention, pause, failure or uncertainty. No extension can reopen a done or skipped invocation.
- **Cancel:** existing cancellation wins once its intent is committed; cancel live owned work, mark remaining unstarted work cancelled, ignore late judge decisions. Preserve already committed skip/history records and outputs. If break committed first, cancellation cannot erase that decision; it can cancel remaining work elsewhere. Do not report cancellation complete until cleanup is known or explicitly confirmed under current rules.

All fresh stop/extend requests target the invocation current at serialization, as today; no expected-path API is introduced. Accepted request IDs bind original paths and replay before lifecycle eligibility checks, including after parent advance. Independent holds remain independent, even when a local stop or override resolves one barrier.

### 8. Integration with the current main baseline

Submission retains the current full-graph installation/readiness preflight, 503 rejection before execution/request-ID acceptance, and concurrent-admission serialization. Recovery performs structural/version checks first; recognized terminal history is not preflighted. The listener stays responsive while supported recovery preflight runs, and checking/failed diagnostics retain their current meaning. Legacy activation refusal and unknown/corrupt-schema startup failure happen before environment checks.

Resume, eligible target retry, later candidate batches and owned-run entry retain all existing gates and stale-revision checks. A currently skipped task is still a recovery candidate when an unfinished enclosing loop can re-arm it; a permanently skipped task under a completed root is excluded. The new skip/decision transitions do not waive preflight, clear unrelated managed failure latches, or change one-shot command semantics. No new backend preflight capability is introduced here.

For configuration errors, syntax/type/duplicate-key errors come first; then removed until_task/on_verdict diagnostics precede a version-1-loop rejection. There is one nonterminal admission per session regardless of definition version.

The lifecycle requirement is explicitly renamed from Loop conditions admit human intervention to Loop controls admit human intervention. Existing scenario titles containing condition remain only as stable OpenSpec regression identifiers: the installed validator rejects dropping/renaming a scenario in a MODIFIED requirement. Their bodies use control semantics or explicitly describe legacy cases. For the same reason the two inherited override scenarios remain under the confidence requirement as integration regression checks; the authoritative override eligibility/action rules and detailed scenarios reside under Held verdicts admit manual override. The barrier rule is normative only in Control tasks form complete phase barriers; validation scenarios exercise it by reference.

### 9. Alternatives and scope trade-offs

| Alternative | Assessment |
| --- | --- |
| `before`/`after` conditions | Easy special cases but duplicate the same control primitive and cannot express an intermediate gate or several decisions. |
| Keep terminal `until_task` plus preflight selectors | Preserves compatibility but leaves queue duplication/context plumbing and cannot skip a suffix. |
| Allow arbitrary concurrent controls and cancel losers | Cannot roll back MR comments, pushes or external effects; race-dependent winner and recovery semantics. Excluded. |
| Require every raw task to reach/be reachable from c | Safe but rejects useful nested modules with independent internal tasks; projected barriers plus subtree admission preserve encapsulation. |
| Use projected comparability without admission | Unsound: an independent child root can execute before its parent's control. Rejected. |
| Always stop after a completed pass | Requires another tail check and makes head guards insufficient. Natural repeat plus explicit cap policy is more uniform. |
| Continue under old persisted semantics via a second engine | Smooth upgrade but doubles lifecycle/recovery paths and preserves a superseded model. Choose explicit operational migration instead. |
| Auto-convert old snapshots | Hashes, in-flight decisions and history would be reinterpreted. Rejected even for apparently mechanical terminal conditions. |
| Use last successful producer on early exit | Convenient reports but silently stale data. Use exact final context and explicit historical sections. |
| Make skipped dependencies acceptable by default | Could dispatch consumers without required MR/results. Defer optional inputs rather than weaken `needs`. |

## Risks / Trade-offs

- [Less concurrency in controlled scopes] -> Parallelize within phases; unrelated root work stays concurrent. Document that barriers wait for full child invocations and no early quorum is added.
- [Projected admission orders more work than a single needs edge] -> Document the child-as-unit contract in v2, test hidden child roots, and include migration diagnostics/examples. Keep exact input manifests separate from ordering edges.
- [Final queue pass has no review result] -> Require reporters to choose an always-executed producer or handle aggregation outside this change; explicitly test unavailable final dependencies.
- [Graceful stop differs from break at a held middle control] -> Expose mapped and effective actions and retain stop as finish-current-pass, not a covert skip operation.
- [Snapshot/ledger size grows] -> Bound automatic passes, retain readable records, avoid duplicating response contents; no compaction feature is introduced.
- [Operator retries can repeat external effects] -> Preserve interrupted-work confirmation, immutable consumed inputs and decision fences; do not promise exactly-once effects.
- [Legacy active execution blocks upgrade] -> Preflight guidance and old-binary completion/cancellation; preserve all original files rather than inventing a migration.

## Migration Plan

1. Ship parsing, schema/view support and new scheduler together with documented breaking changes. Before upgrading a state directory, inspect it with the existing binary and finish or cancel all nonterminal loop executions. Back up the directory; keep that binary available.
2. For new YAML set version 2, move each old loop `on_verdict` map to the old condition task's `control`, delete `until_task` and loop `on_verdict`, retain cap/exhaustion. An old unique-sink condition is a valid tail barrier. Change a continue to proceed only when deliberately choosing to run a suffix. Review nested admission and final-result availability before adopting head/middle controls.
3. Schema-1/2 loopless executions keep existing read/write/recovery behavior. All schema-2/3 executions containing loops are legacy: terminal history is read-only, displayed with its original fields and `execution_supported: false`; nonterminal recovery refuses activation with execution ID/schema and instructions to finish/cancel using the prior binary. Do not downgrade, rewrite, auto-cancel, relabel or feed them to v2 scheduling. Only structurally valid terminal records in recognized legacy schemas avoid blocking startup. Unknown-schema/corrupt records fail startup even if they claim terminal state; they are not fabricated as read-only history. New legacy mutation requests return 409, but exact previously accepted stop/extend/retry/override requests replay their saved route result read-only before compatibility rejection, without preflight or activation; conflicting reuse returns 409. Nonterminal legacy startup refusal still takes precedence over starting an API, so this replay exception cannot make that process serve HTTP.
4. Legacy submission replay preserves its original hash and response without scheduling, and does not cross-match version 2. No API automatically clones old side effects into a new execution. A manually converted YAML submission requires a new request ID and starts a new execution after the operator has accounted for previous work.
5. Rollback requires an older binary and an untouched old directory/backup. Old binaries reject schema 4. Do not edit a schema number or point an old binary at active v2 state expecting conversion.

The deltas replace obsolete condition clauses and scope legacy serialization guarantees to their supported legacy versions. No production code or example files are changed during this proposal.

## Review verification (2026-09-27)

Strict change validation passed both in the original worktree and in an isolated copy of the `1c444a2` main specs. A read-only OpenSpec merge build applied the deltas in that copy without warnings, including the lifecycle requirement rename. The audit confirmed all 17 untouched requirements in the three affected capabilities remain unchanged, and explicitly checked preservation of the current submission preflight, observation diagnostics and judge threshold contract. No main specs, implementation files, Git refs or archived changes were modified. Historic scenario titles were retained for replacement-validation compatibility as explained above.
