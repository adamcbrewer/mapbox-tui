# A bounded CLI/MCP contract repair

This is candidate 2 in [#109](https://github.com/adamcbrewer/mapbox-tui/issues/109),
after the import recipe in #108. It checks whether the existing runner can take
another task without a new execution system. Pi proposes the patch; our code
checks the exact saved patch separately. Passing tests doesn't mean Adam has
approved it. The [shared glossary and file guide](https://github.com/adamcbrewer/mapbox-tui/issues/111)
explains fixtures, fingerprints and receipts.

Adam selected this contract in chat on 28 October 2026, before implementation.
The source is the same frozen commit as the import task, recorded in `task.json`.
This is a deliberately planted regression, not a claim of an open application bug.
No application source or public contract changes in this branch.

## The intended behaviour

The authority is the documented CLI default in [usage](../../docs/USAGE.md#cli-reference)
and the explicit-selector rule in [agent usage](../../docs/AGENT_USAGE.md#standalone-mcp),
confirmed by Adam. The original source also passes these expectations. A schema
helps describe arguments, but cannot decide which behaviour the user intended.

| Input or output | Required behaviour |
| --- | --- |
| CLI `inspect FILE` | Return recorded point 0, including for a one-point file. |
| MCP `inspect_track` | Require exactly one explicit `point_index` or `distance_m`. Omission is an error. |
| Explicit selectors | Preserve zero-based indices, distance zero and option placement before/after the CLI filename. |
| Invalid selectors | Negative/out-of-range index, negative distance and both selectors fail through both interfaces. MCP also rejects a string index. |
| Structured inspection | Expected point index and coordinates, matching CLI JSON and MCP structured/text content. |
| Offline rendering | Matching map/scene JSON, expected dimensions, nonempty ASCII map and no ANSI escapes. |

Parity alone isn't the answer: both interfaces could return the same wrong
point. Fixed expected values are checked independently of their agreement.
The different omission rules are intentional, so making MCP silently default
would be a contract change, not this repair.

Only `go/cli.go` may change. No new files. Schemas, documentation, public tests
and separate checking examples remain read-only. The agent sees only its project
copy, not the private examples or known repair. Those extra examples are held
back from the repair model and must not be used to tune its prompt.

The visible examples use invented GPX coordinates. Extra examples cover a
one-point file, options after the filename, distance zero, invalid selectors and
another offline-map check. Every subprocess gets temporary config, cache, home,
Mapbox and runtime directories; credentials and personal activity data aren't used.

This recipe targets the standalone MCP process workspace. `load_track` doesn't
load an on-screen map. No `viewer_*` mutation, discovered session or live viewer
is needed. Missing-session, stale-revision and partial-operation control cases
belong to a future live-control recipe using test-owned viewers; this task makes
no claim to cover them. Existing repository control tests still run.

## Prepare and calibrate without a repair model

Use the existing prerequisites and immutable prepared image from
[the import recipe](IMPORT_RECIPE.md#run-one-complete-attempt). No installs,
new tools, network permissions or model-setting changes are needed. Host Go must
meet `go.mod`; the existing sandbox image uses Go v1.27.1.

```sh
python3 tools/harness/case_suite.py --prepare --catalogue cli-inspect-v1
```

Set `SUITE_DIR` to the printed directory and `IMAGE_ID` to the existing full
`sha256:` image ID. Then:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/case_suite.py \
  --calibrate SUITE_DIR --image IMAGE_ID
```

Expected exit 0. The readable `calibration.md` should show original/known repair
passing both test sets and planted/ineffective code failing both. These failures
are deliberate. The ineffective edit changes the bad default from 1 to 2.
Calibration supplies all changes itself and makes no repair-model calls.

## Run the agreed task

Keep the same local model settings used for the import workflow. Freeze a plan:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/workflow.py --prepare \
  --suite SUITE_DIR --case cli-inspect-v1 --image IMAGE_ID \
  --config tools/harness/pi/config.local.json --mode plain
```

Read `PLAN_DIR/plan.md`, then run:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/workflow.py --run PLAN_DIR
```

The original limits remain ten model requests, thirty tools, at most two
candidate checks, two minutes per provider request and ten minutes overall.
Plain mode doesn't add automatic repair prompts or transport retries.

The eight independent checks cover original, planted and candidate code on
visible and separate examples, then candidate formatting and `make test`.
Before/after CLI and MCP responses are in the corresponding check stdout logs.
The receipt binds these logs, source trees, task and patch by their fingerprints.
A model saying "done" cannot replace any check.

A deliberate failed repair can use the same Pi tools without a repair model:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite SUITE_DIR --image IMAGE_ID --demo wrong --verify
```

Expect exit 1 and a failed candidate contract check. Original passes, planted
fails as expected, candidate fails; later checks stay unrun. An ambiguous task
must instead stop before model execution until its intended contract is agreed.
Don't pick whichever side is easier to change.

## Stop, inspect and review

Use `operator_log.py RUN_DIR stop` to cancel and preserve evidence. Inspect with:

```sh
python3 tools/harness/workflow.py --report RUN_DIR
python3 tools/harness/report.py
```

Start with `workflow.md` or `normal-tools.md`, then the candidate patch and
linked separate check receipt. The Extend overview links these readable reports;
opening it doesn't revalidate them. Keep saved plans, suite, logs, run records
and patch together under ignored `reports/harness/`. All run evidence is local.
The existing one-use plan, stop and restart rules from the import recipe apply:
a stopped conversation cannot resume after a crash. A retry needs a new plan;
earlier work remains counted. No new recovery guarantees are introduced.

Compare with the retained import run using the same model, reasoning, request
limits, tool allowance, image and plain mode. Separate task difficulty and prior
familiarity from runtime differences. One synthetic run per recipe cannot show
which workflow is better, typical repair quality or human time saved. Missing
attention/review time and billing remain unknown.

## What needed extending

The real obstacle was a source filename embedded in task preparation, native
snapshot capture, independent source reconstruction and the initial prompt.
Those paths now use the registered task's one allowed source file. Existing
import tasks retain their defaults and pinned inputs. The CLI recipe supplies
its own seed, frozen expectations, fixtures and ineffective-edit example.

The runtime, Pi tools, budget enforcement, one-use plans, independent check
sequence, operator records and stopping rules are reused. No general plugin
system, multi-file repair API or live-control capability was needed. Legacy
internal evidence filenames still contain `import`; keeping them preserves
existing receipt compatibility. They don't grant parser-edit permission.

The learning checkpoint awaits Adam's review. Discuss why parity can pass with
two wrong interfaces, which source establishes the intended contract, and which
changes belonged in the recipe versus the shared runner. Do not mark the issue
complete or start later stages merely because checks pass.

## Preparation findings retained locally

The first model attempt was cancelled after review found private-case branches
inside the visible test script. It is retained and excluded from held-out claims.
The scripts were separated and private data changed before the retained attempt.
A supplied wrong repair then exposed a Python cache file written into the
writable project. The scope checker refused that extra file. Running the fixture
helper with Python `-B` prevents the cache without widening repair permissions.
These are fixture corrections, not changes to the agreed contract or model settings.

An initial repeat of the import calibration stopped when Podman reported
`stopped` rather than the required `exited` state. The container was removed;
the result remains blocked. A separate repeat is recorded rather than rewriting
that outcome. See local `reports/harness/issue109-handoff.md` for the final
retained results and earlier excluded evidence. No raw telemetry is committed.

## Retained results, 28 October 2026

The final calibration matched all eight original/planted/reference/ineffective
outcomes, and all nine containers were removed. The current import task also
matched all eight outcomes in a separate repeat, with nine containers removed.

Plain Pi using the unchanged Terra subscription settings proposed a CLI-only
patch. Its exact saved candidate passed all eight independent checks in
321.0407 seconds, using 7 model requests and 11 tools.
All 10 run/checker containers were removed. This establishes one checked
synthetic repair, not general repair quality or Adam's approval.

The supplied default-of-2 repair failed its candidate check as intended, exit 1,
in 135.4849 seconds. Original code passed and the planted bug failed;
later checks stayed unrun. No repair model ran; all five containers were removed.
Both recipes use matching model settings, image, budgets, reasoning and plain
mode. Their different tasks and preparation history prevent a performance claim.

Local starting points are `reports/harness/issue109-handoff.md`,
`reports/harness/issue109-verification.json` and the Extend overview generated by
`report.py`. The successful readable run is
`reports/harness/workflows/runs/d0b0f206b21240fa876d1a0ebfb9b0a7/workflow.md`; the deliberate failure is
`reports/harness/native-tools/runs/519e1b1e562848269421dbe8d68fa701/normal-tools.md`.
The checkout passed 343 Python tests, 32 SDK tests, four Python-to-Pi integration
tests and `make test format-check`. No viewer source changed, so no terminal
interaction check was run. The learning checkpoint remains awaiting review.

## Review-stack follow-up

Review found that managed retry reports linked every attempt to the final patch,
and CLI probe/timeout reports described a repair they never made. Attempt links
now target their own frozen patches; repair text appears only for repair demos.
The new regressions failed before these changes and pass afterwards. The full
Python suite now passes 345 tests, and `make test format-check` passes. No new
repair-model run was needed. The original checked repair remains valid; the
human learning checkpoint still awaits review. The consolidated local report is
`reports/harness/issue109-review-stack.md`.
