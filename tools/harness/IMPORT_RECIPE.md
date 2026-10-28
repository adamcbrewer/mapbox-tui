# A bounded import repair

This is the first practical workflow in [#108](https://github.com/adamcbrewer/mapbox-tui/issues/108),
after the comparisons in #107. Reuse plain Pi, the existing sandbox and the
independent checker. Pi investigates and proposes a repair. Our code freezes
that patch and checks it in a separate copy. Passing checks still needs my review.
The [shared glossary and file guide](https://github.com/adamcbrewer/mapbox-tui/issues/111)
explains patches, fixtures, fingerprints and receipts.

On 28 October 2026 Adam explicitly agreed to proceed with supervised use after
being asked about the #107/#111 learning gate. That authorises this stage;
it doesn't supply missing discussion answers, measured review time or approval
of any proposed repair. Keep #108 open until its own review is complete.

## Start with the calibrated synthetic task

Use `segment-distance-v1`. This deliberately planted bug counts travel across
gaps between recording sections. All example coordinates are invented. It is
an already familiar development task, so a successful repeat doesn't establish
performance on a new bug or save a fresh human-baseline measurement.

The task uses the pinned source commit in `task.json`, not the current checkout.
Only `go/import.go` may change. Existing tests, the added visible test
`go/import_calibration_test.go` and the private checking examples are read-only.
No new files are allowed. Don't apply the resulting patch to current `main`
without checking its applicability and reviewing it.

| Contract | Expected result |
| --- | --- |
| Visible GPX examples | Continuous and separate-section journeys retain their points and sections. Distance includes travel within sections only. |
| Separate checking examples | Single-point sections, three sections, TCX and KML obey the same rule. |
| Geometry and items | Preserve coordinates, section boundaries, waypoints and item counts. Don't merge sections to hide the gap. |
| Missing values and zeroes | Missing coordinates/elevation stay missing. Valid zeroes remain valid. |
| Invalid inputs | Keep existing parse errors, FIT integrity checks and recording-point/item limits. Don't accept corrupt input to make a test pass. |
| Shared behaviour | Existing CLI/MCP, offline import and plain-rendering tests must pass. |
| Saved workspaces | Preserve source fingerprints, changed/missing-file handling and partial-open behaviour. |

The last four rows rely on existing repository tests as well as the focused
examples. They are constraints, not a claim that every possible input is tested.
The frozen base predates later project changes. Check current-source regressions
again before adopting any repair against today's code.

Prepare the suite without a model call:

```sh
python3 tools/harness/case_suite.py --prepare --catalogue segment-distance-v1
```

Use the printed directory as `SUITE_DIR`. With the existing prepared immutable
image ID as `IMAGE_ID`, calibrate the checks:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/case_suite.py \
  --calibrate SUITE_DIR --case segment-distance-v1 --image IMAGE_ID
```

The printed `calibration.md` should show original code and the supplied known
fix passing both check sets. The planted bug and ineffective edit should fail
both on the gap-distance assertion. Those failures are deliberate. Calibration
uses Go containers but no repair model; its overall exit should be 0.

Keep the suite's `suite.json`, `suite.sha256` and `cases/` directory. They retain
the source archive fingerprint, exact fixture/test fingerprints, planted change
and reference repair. Never expose `private/` to the repair agent.

## Run one complete attempt

Prerequisites are Python 3, host Go v1.27.1 on PATH, Git with the pinned history,
the existing Node/Pi installation and signed-in local configuration, rootless Podman with delegated
cgroups, and the prepared Go v1.27.1 image. This recipe installs nothing. The
calibration records the host Go version separately from the image toolchain. The
Go sandbox has no network and uses cached dependencies. Model access occurs
outside that sandbox through the existing configured provider.

Freeze a plan using the existing local model settings:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/workflow.py --prepare \
  --suite SUITE_DIR --case segment-distance-v1 --image IMAGE_ID \
  --config tools/harness/pi/config.local.json --mode plain
```

Read the printed `PLAN_DIR/plan.md` and `plan.json`. The plan fixes the exact
task, model/settings, prompt, tools, image and checking rules. Existing limits
are ten model requests, thirty tools, two candidate checks and ten minutes
overall including verification, with two minutes per provider request. There
are no transport retries or automatic repair prompts in plain mode.

Run the complete attempt with one explicit local command:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/workflow.py --run PLAN_DIR
```

The command prints `RUN_DIR`. Use `operator_log.py RUN_DIR status` to read progress.
Record actual check-ins, monitoring, away and review intervals with the controls
in the [workflow guide](README.md#run-plain-pi-or-the-managed-repair-loop).
Don't infer Adam's attention from a running process or an assistant checking it.

The checker runs eight required checks: visible examples on original, planted
and candidate code; separate examples on those same three versions; candidate
formatting; and candidate `make test`. A checked repair exits 0. A failed repair,
timeout, cancellation or environment blocker exits 1 and retains its evidence.
A model claiming success cannot override those results. A failed planted-code
check is expected evidence that the test detects the bug.

## Stop, inspect and return to manual work

```sh
python3 tools/harness/operator_log.py RUN_DIR stop
python3 tools/harness/workflow.py --report RUN_DIR
```

Stop cancels this run and preserves its patch and logs. Reporting validates saved
evidence without inference. Keep `workflow.md`, `result.json`, `events.jsonl`,
`candidate.patch`, `attempts/`, the referenced independent verification folders,
the saved plan and suite together under ignored `reports/harness/`.
The readable report is the starting point; raw logs are for investigation.

`python3 tools/harness/report.py` builds the shared Use overview at
`reports/harness/use.html` and updates `index.html`. It links to the current AI
attempt and supplied ineffective repair, preserving their readable reports and
run files in the stage snapshot. The overview doesn't revalidate either result.
Plans and other referenced evidence may still need the original local folders;
use `workflow.py --report RUN_DIR` for validation.

This workflow continues a conversation within one process, but it cannot resume
one after cancellation or a crash. The one-use plan stays spent. To try again,
inspect the failure, prepare a new plan and retain the earlier attempt. This is
a new attempt, not a continuation with reset limits. The separate journalled
recovery examples don't establish crash recovery for this workflow.

The agent works in a disposable copy. It doesn't edit the main checkout or apply
its patch there. After stopping, ordinary manual development can continue in
the original checkout. Review `candidate.patch` before deliberately applying it
in a suitable branch; don't delete evidence while it is still needed for review.

## Select a real bug only after triage

The open-issue import search on 28 October 2026 found no suitable maintainer-triaged
import regression. #82 is a deferred offline-bundle feature, not a repair task.
The real-bug run is blocked on an agreed reproducer and expected behaviour.
Don't invent a bug, relabel this synthetic task or weaken its scope to fill that gap.

For a real task, record the issue, public/minimised fixture provenance and SHA-256,
exact starting commit, expected geometry/items/errors, allowed parser files and
immutable test/check files before launch. Decline private activity data, ambiguous
expected behaviour, network-dependent reproduction, broad format redesigns or
changes that exceed the existing runner's one-parser-file contract. Unsupported
tasks need a reviewed runner change before execution. The existing registered
task checker cannot accept arbitrary fixtures or a different base commit.

My review must compare the patch with the reproducer, inspect the independent
results and record corrections and review time separately. Keep synthetic and
real evidence separate. Keep raw prompts, personal paths, coordinates and
telemetry local; public handoffs should contain only sanitised summaries.

After supervised use, decide whether to stay manual. No failed-CI trigger is
implemented here. Any later opt-in trigger needs a reviewed follow-up covering
trusted selection, duplicate suppression, budgets and a kill switch. Merges
remain under human review.

## First supervised-use slice, 28 October 2026

Calibration matched all eight expected outcomes, including the deliberately
broken and ineffective variants. The subsequent plain-Pi attempt using the
existing Terra settings passed all eight independent checks in 294.7423 seconds,
with four model requests and six tools. All ten run/checker containers and all
nine calibration containers were removed. The candidate restores the section
boundary condition in one line and matches the known calibration repair.

Local evidence starts at `reports/harness/issue108-handoff.md` and
`reports/harness/issue108-verification.json`. The run's readable report is
`reports/harness/workflows/runs/a31d6985625e4a5fa74a1bda81481a54/workflow.md`.
The current checkout also passed `make test`, 34 focused harness tests and 34
report tests. No application/parser code changed in this branch.

A separate supplied ineffective repair was also run through Pi's actual tools
and independent checking. It exited 1 after 115.2004 seconds: original code
passed, the planted bug failed and the candidate failed the same import check.
Later checks weren't run. All five containers were removed. No repair model
ran for this deliberate failure. Its readable report is
`reports/harness/native-tools/runs/5c128f19939e4d7a9c2f504833f5f4e5/normal-tools.md`.
To repeat it against a prepared suite and image:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite SUITE_DIR --image IMAGE_ID --demo wrong --verify
```

Expect exit 1 and "Candidate did not restore the expected import behaviour".
This is a deliberate repair failure, not an environment blocker. A final full
harness run passed all 335 Python tests. The successful receipt was revalidated
without another model run. Final local review found no actionable defect in
the recipe/progress changes after correcting the progress-stage label.

This completes the synthetic slice. Real-bug use, Adam's patch review,
discussion answers and the trigger decision remain open. Human attention/review
time and billing are unknown. The known task was reused; this result doesn't
establish time saved or performance on unfamiliar bugs.

## Review and decision still needed

The recipe supplies the import contract, fixture selection and refusal rules.
The runner, sandbox, budgets, one-use plans, independent checker and operator
controls are shared code from earlier stages. No new execution path was needed.

Task preparation and checking were performed by the assistant. Adam authorised
the stage; his task-definition effort, attention and review time weren't measured.
There were no in-run repair corrections. During preparation the report tests
caught an unsupported progress-stage label, which was corrected to `Use`.
Record Adam's patch corrections and discussion separately after review.

My recommendation is to keep submission manual. One familiar synthetic repair
and deliberate failures don't justify a failed-CI trigger. This recommendation
is awaiting Adam's decision; no follow-up automation issue or trigger is created.

For the learning review, compare the successful candidate with the deliberately
ineffective one. Explain why the broken baseline must fail, why an unchecked
model claim can't count as a repair, and why a checked repair still needs human
review. Then record whether the refusal rules and the evidence are sufficient
to try a real, agreed bug when one becomes available.
