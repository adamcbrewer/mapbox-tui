# A bounded viewer interaction repair

This is candidate 3 in [#110](https://github.com/adamcbrewer/mapbox-tui/issues/110),
the final workflow in #102. Adam selected the planted modal/import regression,
then requested the full remaining implementation in chat on 29 October 2026.
That authorises continuing beyond the initial calibration slice. It does not
record answers to the learning questions or approval of the final patch.
The [shared glossary and file guide](https://github.com/adamcbrewer/mapbox-tui/issues/111)
explains fixtures, receipts and the earlier stages.

Pi proposes a repair using its normal tools. The shared harness freezes the
result and checks that exact patch separately. A successful tool call, an
accepted operation and a correct completed import are different claims.

## Agreed behaviour and scope

The visible input sequence was fixed before implementation:

1. Start an empty, test-owned viewer at 120×40, offline with no provider.
2. Check an unavailable viewer, a missing-file import without a partial document,
   and a stale-revision rejection.
3. Press Ctrl+K and wait until Commands is open.
4. Submit the synthetic two-point GPX through attached MCP with `wait: false`.
   Record its `pending` response and operation ID.
5. Poll that operation. Within ten seconds it must reach `applied`, with one
   document, two visible points and the imported item selected. Commands stays open.
6. Wait for the visible import, retain before/after captures and state snapshots,
   then confirm removal of the test-owned tmux server and discovery records.

The planted guard drops asynchronous import completion while a dialog is open.
The viewer stays responsive but the accepted operation remains pending and no
document appears. This is a manufactured regression, not an open product defect.
Increasing the timeout does not restore the dropped completion.

Only `go/control.go` may change. No new source files. Schemas, public tests,
terminal helpers and the probe are frozen. The shared task uses the same pinned
source commit as the earlier recipes, recorded in `task.json`; its control source
and terminal helpers are identical to the initial host calibration's newer base.
The agent receives the visible case. Extra modal/terminal examples and the known
repair stay outside its project. Do not tune prompts using those extra outcomes.

Every viewer has its own tmux socket and temporary home, settings, cache, data
and runtime directories. Tokens are empty, the Mapbox CLI is unavailable and the
provider is disabled. TERM=xterm-256color, COLORTERM=truecolor, NO_COLOR unset;
actual pane dimensions and a monospace font are checked. No personal viewer is
discovered or attached. A missing prerequisite or uncertain outcome blocks the
check instead of counting as detection of the selected bug.

## Prepared terminal image

Use the existing [harness image preparation](README.md#container-execution-draft)
and [terminal prerequisites](../../docs/DEVELOPMENT.md). Host Go must meet
`go.mod`. `ViewerContainerfile` derives a separate image from the existing immutable
prepared image and adds tmux, librsvg, fontconfig and DejaVu Sans Mono. It gives
the container's test account `/bin/sh` so the existing terminal scripts can run;
no host account or desktop setting changes.

Image preparation uses the network. Execution never pulls images or downloads
modules. Use the full immutable ID of the existing image, not a moving tag:

```sh
podman build --pull=never --build-arg HARNESS_IMAGE=EXISTING_FULL_IMAGE_ID \
  -f tools/harness/ViewerContainerfile \
  --iidfile reports/harness/viewer-image.id tools/harness
```

The build records installed package versions. Retain its log and use its resulting
full `sha256:` image ID for calibration, the supplied failure and the AI run.
No npm dependency is added. Existing Pi lockfile installation, disabled lifecycle
scripts and cooldown remain unchanged, following the required
[npm security guidance](https://github.com/lirantal/npm-security-best-practices).

Containers keep the existing non-root identity, no network, read-only input,
seccomp, no capabilities, private namespaces and resource limits. The full viewer
repository check additionally gets a 128 MiB, non-executable tmpfs at
`/workspace/reports`; bin output remains in its existing tmpfs. No new writable
host mount or terminal access is granted. Selected captures leave the container
as bounded, validated log data. Names and sizes are checked before retention,
and receipt validation checks their hashes against the original logs. Generated
reports are excluded from the viewer source snapshot like build output; protected
code and helpers remain checked. The controlled environment disables Python
bytecode generation so ordinary test imports do not create forbidden cache files.

## Calibrate before asking Pi

```sh
python3 tools/harness/case_suite.py --prepare --catalogue viewer-modal-v1
```

Set `SUITE_DIR` to the printed directory and `IMAGE_ID` to the prepared full ID:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/case_suite.py \
  --calibrate SUITE_DIR --image IMAGE_ID
```

Expect exit 0 and all eight outcomes matching in `calibration.md`: original and
known repair pass visible and extra cases; planted and ineffective code fail
both. These failures are deliberate. This command supplies the edits itself;
no repair AI runs. `captures/CHECK/CASE/` contains retained PNG/SVG/TXT files,
initial MCP responses, synthetic GPX and structured state snapshots.

## Run the shared repair workflow

Keep the same local model configuration as the earlier recipes:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/workflow.py --prepare \
  --suite SUITE_DIR --case viewer-modal-v1 --image IMAGE_ID \
  --config tools/harness/pi/config.local.json --mode plain
```

Read the printed plan, then run:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/workflow.py --run PLAN_DIR
```

The model, reasoning, ten-request limit, thirty-tool limit, two possible candidate
checks and two-minute provider-request limit are unchanged. The viewer task gets
twenty minutes overall, including independent checking. The earlier recipes had
ten minutes. The extra time covers full terminal interactions and repeated isolated
builds; it is not evidence of better efficiency. No timing comparison may hide
that budget or the changed image/workload. Plain mode adds no automatic retry.

The eight required checks cover original, planted and candidate code against
visible and separate examples, then formatting and the full repository command
`make test interactions`. This runs both existing terminal scripts on the exact
candidate inside the restricted container. Their bulk temporary screenshots are
not exported; the selected task's before/after captures are retained separately.
The checker stops at a failed candidate; later checks remain not run.

A supplied ineffective repair demonstrates failure without another repair model:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite SUITE_DIR --image IMAGE_ID --demo wrong --verify
```

Expect exit 1: the edit leaves modal imports broken. Original passes, planted
fails as expected, and candidate fails. A missing executable or terminal tool
instead produces a blocker. It must never be described as a rejected repair
based on observed product behaviour.

## Evidence, stop and recovery

Use `operator_log.py RUN_DIR stop` to cancel the owned run. The existing container
cleanup and one-use plan rules apply. Interrupted conversations do not silently
resume or replay edits. A new attempt needs a new plan, and earlier work stays
counted. No new crash-recovery guarantee is introduced.

```sh
python3 tools/harness/workflow.py --report RUN_DIR
python3 tools/harness/report.py
```

Start with `workflow.md` or `normal-tools.md`, then the exact candidate patch,
separate receipt and capture links. Report generation revalidates the saved
workflow; opening the Extend overview alone does not. Keep the suite, plans,
logs, receipts, captures and conversations together under ignored
`reports/harness/`. Raw telemetry and captures stay local.

The initial host-only command `viewer_calibration.py` remains available for
fixed trusted original/seed/restoration demonstrations. Its `--binary` option
is a developer probe, not permission to run generated code on the host. All
model-generated code uses the shared container workflow above.

## Review and retention decision

Structured assertions prove completion and imported state. A person still judges
clipping, legibility and map appearance from the captures. A screenshot or model
opinion alone cannot certify correctness. Failed imports must not leave a partial
document; the existing control suite also checks broader partial workspace,
concurrent-operation and crash cleanup behaviour.

Review one successful and one deliberately failed run, including their exact
patches and captures. Discuss whether a failure is a product regression, timing
or environment problem, and whether this workflow saves enough effort to keep.
Small synthetic samples cannot establish real-bug repair quality, general
reliability or human time saved. Human monitoring/review time and execution
billing stay unknown unless explicitly recorded. Final learning review and
acceptance remain separate from automated verification.

## Retained outcome, 29 October 2026

The final plain-Pi attempt passed all eight independent checks, including the
full terminal suite, in 918.6097 seconds with 8 model requests and 14 tools.
All ten run/checker containers were removed. The supplied ineffective repair
failed its candidate check as intended; later checks remained unrun. A real
missing-terminal image stopped as blocked.

Two earlier AI attempts remain counted: one incomplete source snapshot and one
cancelled setup attempt. Generated screenshots and Python bytecode exposed
missing setup. Across all three AI attempts, 19 model requests and 1610.5077
seconds were recorded, excluding development/calibration and other checks.
No general reliability or time-saving conclusion follows from this sample.

The earlier import and CLI/MCP calibrations each matched all eight expected
outcomes, with nine containers removed apiece. After integrating the #111 reporting changes, the checkout passed 393 Python
tests, 32 SDK tests, four actual-Pi integration tests, `make test format-check`
and both `make interactions` scripts. Capture-integrity checks rejected a
modified PNG. Review also corrected capture metadata propagation, task deadline
propagation and seed-file preparation under a Git checkout.

Local starting points are `reports/harness/issue110-handoff.md`,
`reports/harness/issue110-completion.json` and the Extend overview. The success
report is `reports/harness/workflows/runs/46aa631215de4dfda242c8fcb225d565/workflow.md`;
the deliberate failure is
`reports/harness/native-tools/runs/2055f72ed8734daa84a36b3bc310991a/normal-tools.md`.
All telemetry/captures stay local. Keep the workflow opt-in for reproducible
viewer regressions; human visual review, time accounting and final learning
acceptance remain pending.
