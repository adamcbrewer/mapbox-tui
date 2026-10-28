# Developer harness pilot

I want to know whether the extra code around Pi helps it fix bugs and saves me
time. The current stage extends the import workflow to CLI/MCP contract repairs
([#109](https://github.com/adamcbrewer/mapbox-tui/issues/109)). Start with the
[contract recipe](CONTRACT_RECIPE.md) for the agreed behaviour, scope, checks and
stop/restart instructions. Adam selected this stage and contract on 28 October
2026. The [import recipe](IMPORT_RECIPE.md) and earlier comparison notes remain
available. The learning review is still a separate decision.

I've finished the earlier fixed comparisons with Luna. Plain Pi delivered four checked fixes
out of four; managed Pi delivered three out of four. Both passed the two
development runs. On the reserved evaluation task, one managed run spent its
tool allowance before checking. That stays a failure. The automatic retry wasn't
used in these AI runs, so I can't attribute the difference to retry feedback.
I recommend keeping plain Pi. My review and decision remain pending.

The earlier extra-guidance comparison also didn't justify adopting its change.
Clearer timeout feedback helped a small recovery exercise, but that doesn't
establish better bug repairs. The full local handoff is
`reports/harness/issue107-final-review.md`. Raw results stay outside Git.
Earlier Pi setup and live Luna examples are in the [Pi guide](pi/README.md).

## What I measure and what still needs my participation

My normal workflow is to ask an AI to solve the bug and monitor its responses.
I don't need a baseline where I write the repair myself. The human baseline
means me supervising an AI, using scripts and asking for corrections as needed.
Plain Pi still means its normal coding tools inside the same sandbox.

The main measure is how often the exact saved fix passes the independent checks
within the agreed limits without me steering the repair after launch. My final
review still matters and its time counts separately. I also want to record:

- Every check-in and how long I spent monitoring, even if I didn't intervene.
- Corrections, prompts to continue, restarts and requests to run missing checks.
- Active review/correction time, elapsed time, model requests, tokens and known costs.
- Failed repairs, interrupted or unfinished runs, and claims that disagree with the checks.
- Whether I actually left the run unattended, or monitored it without intervening.
- My judgement after review: would I trust this workflow enough to step away again?

A longer run can still be useful if it needs less attention and returns a checked
candidate. Context management, recovery and automatic verification are part of
the workflow being tested. I'll keep task copies, model/settings, environment,
overall limits and final checking rules fixed, and save each workflow's exact
configuration before launch. A whole-workflow comparison measures the combined
effect; it doesn't tell me which individual part caused it.

Stepping away should leave either a candidate with completed check evidence or
a clear stopping report. It doesn't promise every bug will be solved. A clean
stop is useful behaviour, but isn't a successful repair. The current ten-minute
limit also means I haven't demonstrated unattended work lasting hours.

My judgement helps decide whether this is useful. Repeated runs and recorded
interventions show what that judgement is based on. Old runs didn't measure my
attention, so I won't reconstruct it from elapsed time or call it zero. The fresh
supervised Terra session recorded 161 seconds with the replacement watcher open.
That covers part of the latest run and viewing its result; earlier attention,
continuous focus and review time remain unknown.
The distance task and reserved coordinate task have
completed fixed comparisons. These automated runs don't establish my supervised
baseline or time saved. The next timed human session needs another fresh task;
reusing these familiar examples wouldn't establish the same thing.

## How I want these updates written

Use my voice and the `wat` skill's plain-language approach for #107 docs, reports,
notes and summaries. Lead with what happened and why I should care. Say whether an
AI ran, what our code did, what passed or failed, and what's still unfinished.
Explain unfamiliar terms when they first appear. Keep commands and detailed
status codes available, without making me read raw JSON to understand the result.
Don't turn passing tests into a claim that I've approved the work.

## Run plain Pi or the managed repair loop

The supervised baseline used the fresh synthetic `geometry-bounds-v1`
task. Its map limits must enclose the imported geometry without adding unrelated
locations. It has two visible examples and four private examples covering GPX,
TCX and KML, with coordinates and geometry preserved. It belongs to a different
bug family from the earlier XML, latitude and distance tasks.

I selected plain Pi with Terra for this session. Its first attempt passed both
visible examples, then stopped at the twelve-tool allowance before independent
checking. Its own full project test ran out of temporary disk space. This stays
an unchecked failure, with nine requests and every tool outcome retained.
I explicitly confirmed the watcher was open and later said I was still monitoring;
I then clarified I had stopped it earlier without recording when. That session's
timing is unusable; the independently checked repair result remains valid.
The repeat used a fresh timer. Its local live watcher records a start when opened
and an end on Ctrl+C or terminal closure. This measures the watcher-open period,
not continuous focus, and closing it doesn't cancel Pi.

The revised attempts allowed thirty tools and the same ten AI requests and
ten-minute deadline. Its prompt distinguishes the host's mise setup from the
sandbox's plain Go commands. Native builds use the checker's serial, consistent
Go flags so the visible test and full build can share their cache. The memory and
disk limits stay unchanged. A tool-limit stop can still send the exact saved patch
to independent checking within the remaining time, without further inference.
The first failed attempt stays in the record. This is a diagnostic rerun after
several workflow fixes, not a controlled comparison of one change.

The revised Terra attempt passed all eight independent checks in 316 seconds,
using five AI requests and seven tools. Its own full build passed inside the
unchanged resource caps. It corrected one rejected timeout request and received
no in-run repair correction. The thirty-tool allowance wasn't needed in this
attempt. A supplied zero-AI example separately proved that call thirty-one is
blocked and the saved patch can still pass checking after tool exhaustion.

The fresh repeat passed all eight checks in 304 seconds, again using five requests
and seven tools. The replacement watcher recorded 161 seconds starting partway
through this run. Earlier watching time is unknown, and that interval doesn't
establish continuous focus or review time.

These three attempts used different workflow settings, so they don't form a new
paired comparison or establish time saved. Preparation and calibration make no
model calls. For a complete timed session, I need to open the watcher before
inference starts and explicitly record monitoring, away and review intervals.
The plan and local watcher are recorded in `progress.json`; earlier Luna results
stay unchanged.

`workflow.py` keeps Pi's normal tools in the sandbox. Both modes use the same
independent checker. Plain Pi gets no automatic repair feedback. Managed Pi can
receive one retry prompt after a failed candidate check. It keeps the conversation
and the remaining budget, then checks the next saved candidate. It never gets the
private test inputs or known fix.

Each retry keeps separate sandbox setup, tool and removal logs, so its output
doesn't replace the earlier attempt's evidence.

Using the existing prepared image, saved task suite and subscription settings:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/workflow.py --prepare \
  --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" \
  --config /home/adam/localhost/mapbox-tui/tools/harness/pi/config.local.json --mode managed
```

Read the printed `PLAN_DIR/plan.md`. Use `--mode plain` for the comparison version.
The plan fixes model/settings, task, tools, image, checking rules and limits before
inference. It allows one run: ten model requests with the current preferences,
thirty Pi tools, two candidate checks and ten minutes overall. Checks share that
time allowance. Provider requests keep their two-minute limit and no transport
retries. A stopped or failed attempt stays spent.

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/workflow.py --run PLAN_DIR
```

The launch prints the run folder. Use it for these controls, which make no model
calls themselves:

```sh
python3 tools/harness/operator_log.py RUN_DIR status
python3 tools/harness/operator_log.py RUN_DIR check-in
python3 tools/harness/operator_log.py RUN_DIR monitoring
python3 tools/harness/operator_log.py RUN_DIR away
python3 tools/harness/operator_log.py RUN_DIR paused
python3 tools/harness/operator_log.py RUN_DIR reviewing
python3 tools/harness/operator_log.py RUN_DIR finished
python3 tools/harness/operator_log.py RUN_DIR feedback --message "Please check the expected behaviour again."
python3 tools/harness/operator_log.py RUN_DIR stop
```

`status` reads progress and recent assistant responses. A `check-in` explicitly
records that I looked. State changes record my own account of monitoring, waiting,
breaks and review; they don't detect attention automatically. `finished` ends my
timing session. `stop` cancels the running work and preserves its evidence.
Corrections are taken once, after the current conversation stops, while the
original request/tool/time allowance remains. A correction after the run ends is
refused. A completed run's stop control can't signal a later run in the same process.

Attention figures cover only explicitly recorded intervals. Unrecorded time stays
unknown. Watching without intervening and actually stepping away are different
observations. The unattended reference runs don't replace a measured session of
me supervising the AI. Final review and my confidence remain human decisions.

This native-tool workflow keeps context within one process. It doesn't automatically
resume after a process crash. Its saved patches, attempts and logs remain available
for inspection, and the one-use plan stays spent. The journalled recovery examples
below have their own evidence; they don't prove crash recovery for this new path.

To read and validate a completed run without inference:

```sh
python3 tools/harness/workflow.py --report RUN_DIR
```

The report keeps failed candidate attempts, their exact patches and independent
receipts. Model-request exhaustion can still leave a patch that passes checking;
the stopped conversation and the checked patch are recorded separately. A timeout,
cancellation, invalid input or missing evidence mustn't become a success claim.

## Compare fixed workflow versions

`workflow_compare.py` saves plain/managed/managed/plain order as two reversed
pairs. Both use identical broken starting copies and the same prompt, model,
normal tools, image, limits and final checker. The managed retry feedback is the
single difference. Its chosen measure is final checked repairs without delivered
operator corrections; first-repair success is kept separately. Incorrect success
claims block adoption. A tie favours plain Pi.

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/workflow_compare.py --prepare \
  --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" \
  --config /home/adam/localhost/mapbox-tui/tools/harness/pi/config.local.json
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/workflow_compare.py --run EXPERIMENT_DIR
python3 tools/harness/workflow_compare.py --report EXPERIMENT_DIR
```

With the current settings, the plan caps the four runs at forty model requests.
Each cell starts once; there are no replacement attempts. A blocked, cancelled or
invalid run stops the remaining experiment. Read reports after the experiment
stops; use `operator_log.py ... status` for a running cell. Reporting makes no
model calls. Exit 0 means the four attempts finished, not that managed Pi won.

After development choices are fixed, `--case coordinate-latitude-limits-v2` with
the original saved suite selects the reserved task explicitly. It uses a different
bug family and checking examples. Keep both workflow versions unchanged for that
evaluation. Don't use its result to tune the prompt and call that held-out evidence.

The comparison's `claim-review.json` separately records the assistant's inspection
of model statements against actual tool output and checker results. That isn't my
approval. Missing billing and human attention remain unknown. Saved results feed
`evaluate.py` and the later #111 report work without treating a chronological trend
as a controlled experiment.

## Keep every attempt in the results

I don't want a passing retry to hide the failed repair that came before it.
`evaluate.py` reads saved run results and checks their supporting files. It makes
no model calls.

```sh
python3 tools/harness/evaluate.py
PYTHONPATH=tools/harness python3 -m unittest test_evaluate -v
```

The command prints a new `reports/harness/compare/inventories/<id>/inventory.md`.
Read that first. The neighbouring `inventory.json` keeps the saved results,
settings, file fingerprints and all recorded check attempts. A fingerprint
identifies a file's exact contents. Keep the original patches and logs too;
the inventory doesn't copy every supporting file.

If I record watching or review time after a run ends, the inventory reads that
separate record too. It keeps a copy and fingerprint without changing the
original result. If that record can't be read, human time stays unknown and the
repair's checks still count on their own evidence.

One task can contain several checks. I count the task once, including the time
and cost of failed attempts. First-attempt success means one repair passed every
required check. Success by the end of the run can include a retry. A later manual
check stays separate and doesn't rewrite an earlier failure.

These old runs used different settings and tasks. Their totals don't tell me that
the harness improved. Missing billing, token usage, corrections or review time
stay unknown. A focused test passing or the model saying "done" doesn't establish
that all required checks passed.

To try a successful prepared example and a deliberately changed log:

```sh
PYTHONPATH=tools/harness python3 -m unittest -v \
  test_evaluate.VerifiedInventoryTests.test_failed_first_repair_and_successful_retry_count_one_task \
  test_evaluate.VerifiedInventoryTests.test_changed_check_log_prevents_success_without_rewriting_receipt
```

Both tests should pass. The first keeps a failed repair and a passing retry as one
task. The second changes a saved check log and confirms that the old pass stops
counting. These tests supply fake check replies; no model or Go check runs inside
them.

Use `--reports PATH` for another local evidence folder and `--output NEW_DIR` for
a new destination. Existing inventories aren't overwritten. Run this after the
tasks stop, because it doesn't lock active sessions. Exit 1 means evidence needs
attention, including missing/unreadable result files or an unconfirmed pass.
Exit 0 means the inventory found no such problem; failed repairs still stay in it.

## Try automatic checking after the AI runs out of turns

In #106 a fix had been written, but the model had no requests left to ask for
tests. I compared stopping there with letting our code finish the checks.

The script supplied a working fix and a deliberately bad fix to both versions.
The tasks, repairs, test rules, container image and limits were the same. Each
pair ran twice, in reverse order the second time. I saved the plan before running
it and chose one measure: did the run finish with a fix that passed every required
check, within its original limits and without another model request?

No repair AI or Pi session ran. Ten request slots were marked spent to reproduce
the limit. The Go checks ran in real containers.

Using the existing prepared image and rootless Podman setup, from Bash:

```sh
HARNESS_IMAGE_ID="$(cat /home/adam/localhost/mapbox-tui/reports/harness/prepared-image.id)"
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/compare.py \
  --prepare --image "$HARNESS_IMAGE_ID"
```

This checks the runtime, image and Go version, then prints a new experiment
folder. Its `plan.json` saves the inputs, settings, run order and chosen measure.
Copy the printed path into:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/compare.py --run EXPERIMENT_DIR
python3 tools/harness/compare.py --report EXPERIMENT_DIR
PYTHONPATH=tools/harness python3 -m unittest test_compare -v
```

Read `EXPERIMENT_DIR/comparison.md`. I expect automatic checking to finish the
working fix twice, while stopping leaves it unchecked twice. The bad fix must
never count as successful. Its automatic runs can still end `budget_exhausted`:
the check failed, and there are no model requests left for another repair.
"Unchecked" and "checked and failed" are different results.

Changed inputs or an already-started experiment stop execution. If a run is
interrupted, use `--report` to inspect what happened. A deliberate rerun needs a
new plan; keep the earlier attempt. The report checks saved results, file
fingerprints, paired settings and the independent test evidence, including failures.

This prepared example shows that our code can finish checking a working fix.
I haven't shown better AI repairs or time saved for me. Checking takes extra
computer time, and my review/correction time and execution billing are unknown.

## Check the bug tasks before asking the AI to fix them

A bad task could make a good repair look wrong, or let a bad repair pass.
`case_suite.py` makes project copies from one fixed public source commit and
plants two different kinds of bug:

- **Development:** XML prefix handling in GPX/TCX/KML imports.
- **Held-out:** latitude validation. This task is saved for evaluation after the
  development choices are fixed. I won't use its results to tune the prompt.

Each `agent/` project has the broken source and public task. Its sibling
`private/` folder has the planted-change patch, known answer and extra examples.
The future Pi connection must give the model only the agent project. One example
also contains text telling the reader to claim success; it is task data.

```sh
python3 tools/harness/case_suite.py --prepare
```

Copy the printed suite folder into `SUITE_DIR` below. Using the existing image
and rootless Podman setup, from Bash:

```sh
HARNESS_IMAGE_ID="$(cat /home/adam/localhost/mapbox-tui/reports/harness/prepared-image.id)"
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/case_suite.py \
  --calibrate SUITE_DIR --image "$HARNESS_IMAGE_ID"
PYTHONPATH=tools/harness python3 -m unittest test_case_suite -v
```

The command checks the tasks, a step called calibration, then prints a new
`calibration.md`. The original code and known fix should pass both the public
example and the extra private examples. The planted bug and ineffective edit
should fail both. Each failure must match the intended assertion, so an unrelated
error can't make a task look ready. No model calls are made.

Each case has the existing 600-second deadline and container cleanup rules.
Use `--case CASE_ID` with `--calibrate` to check one task. That new batch report
covers only the selected task; earlier attempts stay saved.

The first coordinate draft didn't reproduce its bug: GPX rejected the input
before it reached the check I'd changed. That failed draft is retained. Revision
2 uses KML to reach the intended check. The development task was unchanged, so
I reused its passing result after comparing its exact files and definition.

The suite file records fingerprints for the project and private files. Changed
files and missing required subtests are refused. Results live under
`reports/harness/case-calibration/`; batch reports live under
`reports/harness/case-calibration-batches/`. Keep the projects, results and logs.

I've chosen normal Pi coding tools inside the same sandbox for the plain-Pi
baseline. Connecting those tools and running actual repair attempts are next.
These prepared tasks don't establish AI repair skill, time saved or how a model
reacts to the misleading text. My review is still pending.

The [shared glossary and file guide (#111)](https://github.com/adamcbrewer/mapbox-tui/issues/111)
explains the evidence files.

## Prepare a fresh bug for the supervision comparison

I don't need to wait for a real bug. The `segment-distance-v1` task deliberately
breaks journey distance in a disposable copy. Separate recording sections should
keep their own points and distances; the travel across a gap must not be counted.
The visible example covers a continuous journey and two separate sections.
Private checks cover other section shapes and GPX, TCX and KML examples.
All coordinates are invented.

```sh
python3 tools/harness/case_suite.py --prepare --catalogue segment-distance-v1
```

Copy the printed suite folder into `SUITE_DIR`. Using the existing prepared image:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/case_suite.py \
  --calibrate SUITE_DIR --case segment-distance-v1 --image "$HARNESS_IMAGE_ID"
```

This supplies a known fix; it doesn't ask an AI to solve the bug. The original
and known fix must pass both check sets. The planted bug and an edit that doesn't
fix it must fail on the intended assertion. `calibration.md` explains all eight
results. The original catalogue remains the default, and saved older suites
still load. This new development task doesn't consume the held-out task.

Task preparation and a live comparison are separate. The full checker now
selects the registered task's planted change, visible test and private checking
examples. It still checks the exact saved patch in a fresh copy, then formatting
and the repository tests. The XML task remains the default. Changed task inputs
or checking rules stop the run.

To exercise that connection with supplied repairs through actual Pi tools:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite SUITE_DIR --image "$HARNESS_IMAGE_ID" --demo good --verify
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite SUITE_DIR --image "$HARNESS_IMAGE_ID" --demo wrong --verify
```

Expect the good fix to pass all eight independent checks (exit 0). The bad edit
must fail its candidate test and stay failed (exit 1); later checks should say
not run. The script supplies both edits. These commands don't use a repair AI.
For a standalone saved patch, `verify.py --suite SUITE_DIR --image IMAGE_ID
--patch PATCH_FILE` selects the same registered checking inputs.

Before the live comparison, both workflows still need a saved protocol and a
way to record my check-ins, interventions and review. Both must receive identical
broken copies, with private answers kept outside their sandbox. This connection
doesn't demonstrate a supervised or unattended AI repair. No human timer starts
during preparation.

## Check normal Pi tools inside the sandbox

I chose Pi's normal coding tools for the plain-Pi baseline: `read`, `edit`,
`write` and `bash`. `native_run.py` connects the actual Pi SDK tools to a
disposable project inside the container. The script supplies the calls for now.
No repair AI runs, and this doesn't complete the comparison.

The known fix should pass the public XML import test. The deliberately bad fix
should fail that same test with the planted prefix bug. The boundary check tries
host/private-file reads, normal file edits and commands, network access and a
Python module planted in the project. It checks that our worker keeps running
trusted code while the tools operate on the task files.

From this checkout, with the existing prepared image and the locked Pi
dependencies already available:

```sh
HARNESS_IMAGE_ID="$(cat /home/adam/localhost/mapbox-tui/reports/harness/prepared-image.id)"
HARNESS_CASE_SUITE=reports/harness/case-suites/runs/f13f59cdb94441189474216479b9141e
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" --demo good
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" --demo wrong
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" --demo probe
```

Use the suite path printed by `case_suite.py --prepare` if you're preparing a
new checkout. The saved suite above is local to this worktree. Pi setup is in the
[Pi guide](pi/README.md); these prepared checks need no sign-in. I reused the
installed dependency tree after checking that its package and lock files matched.
I didn't download packages or run install scripts for this step.

Each command prints a new `normal-tools.md` under
`reports/harness/native-tools/runs/<id>/`. Read that first. The neighbouring
session, source snapshot, proposed patch and command logs keep the details.
Exit 0 and `native_tools_ready` mean the supplied sequence matched its expected
results. For `wrong`, that includes the test failing for the intended reason.
They don't mean that a repair passed independent checking. A startup, snapshot,
cleanup or unexpected tool failure stops the check instead.

The host source copy stays read-only at `/input`. Changes happen in temporary
container storage at `/workspace`. Known fixes and private test examples aren't
mounted. Pi's file and command operations go through the sandbox connection;
Node can't launch host commands or use the network. The SDK also needs read
permission for `/workspace/*` to look up edit paths. File contents still go
through the sandbox. Only output files created by the SDK in this run's temporary
folder can be read back locally.

The existing container limits stay in place: no network, an unprivileged user,
no added capabilities, 1 GiB memory and 256 processes. This prepared check allows
12 Pi tool calls, 100 sandbox operations and 600 seconds. Requests are limited
to 64 KiB, reads and command output to 1 MiB. Images aren't part of these text
tasks, so the read tool's image processing isn't enabled. This is normal Pi tool
behaviour within those explicit task limits, not unrestricted host access.

The [npm security guide](https://github.com/lirantal/npm-security-best-practices)
informs this setup: keep locked versions and install scripts disabled, and keep
task execution isolated from host credentials. No dependency versions, release
cooldown or install policy changed here.

```sh
PYTHONPATH=tools/harness python3 -m unittest test_native test_container -v
node --test tools/harness/pi/loop.test.mjs tools/harness/pi/native.test.mjs
```

The Python checks exercise path escapes, limits, tool ownership, project
snapshots and container settings. The SDK checks preserve normal schemas and
edits, retain failed command results and limit local reads to SDK output files.
They use supplied replies. The commands above run the tools in a real container.
The held-out coordinate task stays unused until the development choices are fixed.

## Check Pi's saved fix independently

I can now add `--verify` to a prepared normal-tool run. Pi's tools stop and their
container is removed before our existing checker starts. The checker applies the
exact saved patch to a fresh source copy. Its tests and extra examples stay
outside Pi's project.

I fix the checking rules before starting Pi's tools. Both parts share the original
600-second deadline, and each prepared run permits one independent check. A patch,
source snapshot, task or check log that doesn't match its recorded contents can't
count as a success. Cleanup must also be confirmed.

Using the same image and task-folder variables as above:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" --demo good --verify
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" --demo tailored --verify
```

The known fix should pass all eight required checks and exit 0, labelled
`native_verification_passed`. Those checks cover original, broken and repaired
code on the visible and extra import examples, then formatting and repository
tests on the repair. The deliberately broken versions must fail their tests for
the intended reason.

The `tailored` repair is a misleading shortcut: it handles only the filenames in
Pi's visible examples. Pi's own test should pass, but the checker's different
filenames should expose it. Expect exit 1, labelled `repair_failed`. The
`normal-tools.md` report shows which checks ran, which failed and which weren't
reached. Its neighbouring saved result links the exact independent-check receipt.

These are supplied repairs through real Pi tools and real container checks.
No repair AI runs. This doesn't establish AI repair skill, my approval or time
saved. Execution billing and my review time stay unknown. The held-out task is
still unused; this connection supports the pinned development task only.

```sh
PYTHONPATH=tools/harness python3 -m unittest test_native_check test_native -v
```

Those Python tests supply check outputs. They exercise the link between the
saved change and the checker, including damaged evidence, changed rules,
cancellation and the shared deadline. Keep the actual run logs for evidence
that the Go checks ran.

## Save and run one real plain-Pi baseline

I can now ask a real subscription model to repair the development task using Pi's
standard coding prompt and normal tools. This is one baseline pilot. I haven't
run a matched harness version or established that the harness helps.

`baseline.py --prepare` saves the task, actual standard prompt, normal tool
definitions, exact model settings, image, checking rules and limits. It previews
Pi offline and checks the prepared Go image. It makes no repair-model call.

Using the existing task folder, image and locally selected model preferences:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/baseline.py --prepare \
  --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" \
  --config /home/adam/localhost/mapbox-tui/tools/harness/pi/config.local.json
```

Copy the printed plan folder into:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/baseline.py --run PLAN_DIR
```

The second command can make real model requests. A plan starts one attempt.
Reusing it is refused, even if the first attempt stopped. Changing the saved
plan, task, implementation, actual prompt or tools also stops the run. A new
attempt needs a deliberate new plan; this command doesn't create one for me.

The existing preferences are Luna with subscription access, low reasoning, ten
requests, twelve tool calls, two minutes per request and ten minutes overall.
The saved output-token preference is preserved. No automatic transport retry or
model substitution is allowed. Each request is saved against its limit before
it can be sent. API-key billing isn't a fallback.

Pi's model process can contact the provider and use the existing Pi sign-in file.
It can't launch host commands. All coding-tool operations still go through the
sandbox connection; the project container has no network or credentials. Known
fixes and extra checker examples stay outside it. The read tool can only read
host output files the SDK itself created for this run. The
[npm security guide](https://github.com/lirantal/npm-security-best-practices)
informs these boundaries; locked dependencies, disabled install scripts and the
existing release cooldown stay in place.

The chosen measure is whether the saved patch passes every independent check
inside the original ten-minute deadline. The common checker runs after Pi stops,
gets no extra time and sends no repair feedback to Pi. The conversation's stop
reason and the patch's check result stay separate. A retained patch can still be
graded after a model-request stop if time remains. A claim of success doesn't
count without the matching check evidence.

Read the printed `baseline.md` first. Its folder keeps the exact plan reference,
actual context, conversation, request reservations, patch, project snapshots and
check logs. Tokens come from reported provider usage; missing usage and billing
stay unknown. Model billing, execution billing and my review time are separate.
The held-out task stays unused, and my learning review remains pending.

```sh
PYTHONPATH=tools/harness python3 -m unittest test_baseline -v
node --test tools/harness/pi/loop.test.mjs tools/harness/pi/native.test.mjs
```

These tests use supplied replies. They exercise plan reuse, changed context,
request reservations, limits, normal-tool payloads and failed-session reporting
without a repair-model call.

## Compare the container-tooling guidance

The live pilot wasted calls looking for host tools inside the container. I want
to test one added paragraph that explains which tools are available and that the
host `mise` prefix doesn't apply there. It gives no fix or hidden-test hints.
The original task and approved repository instructions stay intact.

`paired.py` saves four fresh runs: baseline, guidance, guidance, baseline. They
form two pairs in reversed order. The pilot isn't included. Both versions keep
the same standard Pi system prompt, normal tools, task, model, reasoning, image,
request limits and independent checker. The only input change is the saved
guidance paragraph. Preparation checks its tool-availability claims against the
actual container before any model request.

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/paired.py --prepare \
  --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" \
  --config /home/adam/localhost/mapbox-tui/tools/harness/pi/config.local.json
```

Copy the printed experiment folder into:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/paired.py --run EXPERIMENT_DIR
```

This can make four real model attempts. Each keeps the existing ten-request,
twelve-tool and ten-minute limits, including its common checker. The whole
experiment allows at most forty model requests with the current preferences.
It runs in the saved order without replacement attempts. Cancellation, a blocked
environment or invalid evidence stops the remaining runs. A spent experiment
can't start again.

I chose failed Pi tool calls as the main measure before running the comparison.
A useful result needs fewer failures in both pairs, with all four conversations
finishing and all four exact repairs passing every required check. I also keep
requests, total tools, reported tokens and elapsed time. Fewer errors alone can't
make an unchecked repair count as an improvement.

Read `comparison.md` and `plan.md` in the printed folder. The numbered `cells`
folders keep each one-use plan and run identity. Saved receipt fingerprints,
actual contexts, model reservations and independent logs must match before a
passing result counts. Missing runs or usage stay missing; failed attempts aren't
replaced or dropped from the report.

After the experiment has stopped, rebuild the report without inference:

```sh
python3 tools/harness/paired.py --report EXPERIMENT_DIR
PYTHONPATH=tools/harness python3 -m unittest test_paired -v
```

Reading a historical report validates its saved plans and receipts. It doesn't
require today's reporting code to match the old execution code. Starting model
runs still requires the exact saved implementation. The executed source copies
stay with this experiment, and changing the reporter doesn't rewrite receipts.

Exit 0 from reporting means all four runs finished; it doesn't approve the
guidance change. I still need to inspect the model's testing claims and record
my decision. Two pairs on one synthetic task don't establish general repair
skill or time saved. Billing and my review time stay unknown. The held-out task
and human baseline remain pending.

## Explain an invalid timeout clearly

The guidance experiment repeatedly stalled on timeout values that looked like
milliseconds. Pi's Bash tool expects seconds. The old refusal only said the
timeout was invalid, so it gave the model little help correcting the request.

The refusal now states the units, the permitted range (greater than zero and at
most 600 seconds), the default of 120 seconds and an example: two minutes is
120. The original run deadline can still shorten an accepted timeout. The
runner rejects an oversized request; it doesn't guess a conversion or execute
the command with a larger limit.

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/native_run.py \
  --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" --demo timeout
python3 tools/harness/pi_integration_test.py
```

The prepared example sends `timeout: 120000`, expects a refusal, then supplies
`timeout: 120`. The second command checks that the rejected command never wrote
its marker file. Expect exit 0 because that failure and correction matched the
expected results. Read the printed `normal-tools.md` and neighbouring session.
Both calls count against the existing tool limit; no extra model request or
automatic retry is granted.

The integration tests use the actual Pi SDK and Python connection with supplied
check replies. The prepared example uses a real sandbox. Neither asks a repair
AI to choose the correction. These checks establish that the error reaches Pi
and a corrected call works. They don't establish that the clearer message saves
model requests, improves repairs or saves my time.

## Compare recovery from the same supplied timeout mistake

`timeout_recovery.py` saves four fresh live runs: generic error, clear error,
clear error, generic error. Each starts with the same supplied invalid `pwd`
request (`timeout: 120000`). Actual Pi tools refuse it before execution. The
live AI gets that request and its error quoted in the prompt, then chooses its
own correction. The supplied failure isn't an earlier AI action and doesn't
count as an AI mistake. It uses one of the existing twelve tool calls.

Only the quoted feedback and the refusal text differ. The model, standard Pi
tools, task, sandbox image, settings and limits stay fixed. The main measure is
repeated invalid timeouts after the supplied failure. A useful result needs fewer
repeats in both pairs, with every conversation finishing inside ten minutes and
actually executing `pwd` without changing project files. If both versions recover
immediately, I haven't measured an advantage.

Using the existing image, suite and subscription configuration:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/timeout_recovery.py \
  --prepare --suite "$HARNESS_CASE_SUITE" --image "$HARNESS_IMAGE_ID" \
  --config /home/adam/localhost/mapbox-tui/tools/harness/pi/config.local.json
```

Read the printed folder's `plan.md` before starting it:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/timeout_recovery.py --run EXPERIMENT_DIR
python3 tools/harness/timeout_recovery.py --report EXPERIMENT_DIR
PYTHONPATH=tools/harness python3 -m unittest test_timeout_recovery -v
```

The saved plan allows one attempt, with no replacement runs. Ten requests per
run means at most forty across the comparison. A blocked, cancelled or invalid
run stops the remaining work. Every run retains the two-minute request limit,
ten-minute overall limit and original output-token setting. No automatic retries.

`comparison.md` checks the actual sandbox command and output, unchanged project
snapshot, model reservations, frozen prompt and container cleanup. Supplied and
live Pi sessions are saved separately. Changed receipts can't keep an earlier
pass; a spent plan can't start more model calls. Reporting sends no requests.
Exit 0 means all four attempts finished, not that clear feedback won.

This is a small request-recovery test. It doesn't measure bug repair, everyday
work or my time saved. The held-out bug task and human baseline stay unused.
Billing and my review time remain unknown unless measured.

The first saved comparison met that narrow measure. The generic error led Luna
to repeat `120000` once in each run. With clear feedback, it chose `120` straight
away in both runs. All four eventually executed `pwd`, reported `/workspace`
correctly, left the project unchanged and removed their containers. Generic
feedback used six model requests and 7,881 reported tokens; clear feedback used
four requests and 5,298 tokens. This supports keeping the clearer error for this
recovery example. It doesn't establish better bug repairs or my time saved.
My review remains pending. The local comparison is under
`reports/harness/timeout-recovery/experiments/b17e131160584b558dfc7f7429ec15e9/`.

## Recover a prepared session

Recover follows Verify: first we bind checks to an exact repair, then we handle
stopped work without assuming that the last edit failed. This slice uses prepared
replies through the real Pi SDK. No AI model or subscription is used. It supports
the current one-file import pilot; it does not resume a saved conversation. The subscription path below constructs
fresh model context under the same recovery rules.

A journal is a saved record of intended edits and observed results. Here it lives
under `reports/harness/recovery/runs/<run-id>/journal.json`, outside Pi's write
scope. The working copy stays in a separate `/tmp/mapbox-calibration-recovery-*`
directory. Both are retained when the process stops. Don't delete either while
reviewing or recovering a session.

Before writing, Python durably saves the operation ID, exact arguments and before/
after SHA-256 hashes (fingerprints of the file bytes). After writing, it observes
the file and saves completion. On resume, an unchanged file permits the pending
edit; matching after-bytes mean it already happened. Any other content stops as
`conflict`, preserving the file for human review. Completed edits are checked
again too. A series of edits can be partly applied; there is no rollback and no
multi-file transaction. The current tool still exposes only one file per edit.

Python checks the whole saved source copy, task, runner version, frozen checking
rules and earlier check receipts before creating fresh Pi context. `handoff.json`
is a navigation aid, regenerated from those observations. Editing its summary
cannot authorise a replay, expand permissions or make a check pass. Pi's messages
remain separately in `pi-session.json`. Each fresh entry keeps its context,
conversation and stderr under `pi-sessions/<entry>/`. `partial.patch` records the
observed edit even when its reply was lost. If a journal cannot be reconstructed,
`recovery-stop.json` records that refusal without overwriting earlier evidence.
Missing or inconsistent state stops for deliberate reconstruction; the runner does not guess how to rebuild it.

Use the existing prepared image and installed Pi dependencies, as described in
the earlier checkpoint. From this checkout in Bash:

```sh
HARNESS_IMAGE_ID="$(cat /home/adam/localhost/mapbox-tui/reports/harness/prepared-image.id)"
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/recover.py \
  --image "$HARNESS_IMAGE_ID" --fault after-write
```

Expect exit 1 and `incomplete`: the edit was written but its reply was deliberately
lost. The command prints the run ID and retained copy. Copy that ID into:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/recover.py --resume RUN_ID
```

Expect exit 0 and `verified_repair_passed`. Recovery observes the first edit
without writing it twice. The first prepared repair fails its import check;
the second passes all eight independent checks. Even a handoff falsely saying
"all checks passed" cannot skip these checks. These are prepared replies, so the
example proves recovery and tool wiring, not an AI's repair ability.

For a deliberate conflict, start another interrupted run, append a harmless line
to its printed working copy's `go/import.go`, then resume it. Expect exit 1 and
`conflict`; your line must remain. Inspect `journal.json` for the observed hash
and recovery decision, and `result.json` for the outcome. `handoff.json` points to
the next permitted action and any independent verification evidence.

Other `--fault` locations are `before-edit`, `during-verification` and
`before-receipt`. The first can retry unchanged bytes. The last can revalidate
finished checks without rerunning them. An interrupted verification stays
explicitly incomplete and needs human inspection; it is never replayed blindly.
This includes unconfirmed container cleanup after a hard kill. Existing owned
containers have the original time limit, but absence is not assumed.

```sh
python3 tools/harness/recover.py --cancel RUN_ID
PYTHONPATH=tools/harness python3 -m unittest test_recovery -v
```

Cancellation persists a stop request, signals only the recorded matching Python
process, and keeps partial files, patch and logs. Repeating it is safe. A separate
publication lock orders cancellation against saving success: cancellation wins
if recorded first; an already published completion returns "Session already
stopped". The final publisher rechecks source and check evidence under that lock.
Cancelled sessions cannot be resumed into success. A lock prevents concurrent resumes.
The original 600-second deadline includes downtime, 12 tool calls and two repair
attempts survive restarts, and there are at most three recovery entries. There
are zero model requests or transport retries on this prepared path. The existing
live runner retains its request timeout and disabled provider/SDK retries; the
live recovery path is described below. Regular `pi_run.py` commands keep their earlier ephemeral
working-copy behaviour. Use `recover.py` for this slice.

The tests also kill a child process before/after writing, bypassing Python's
cleanup handlers, and demonstrate stale summaries, conflicting human edits,
changed evidence, repeated cancellation and interrupted verification. Unit checks
use prepared test execution; the Podman demonstrations exercise actual checks.
Passing checks do not imply human approval. The learning checkpoint stays pending.

Build the readable recovery report with `python3 tools/harness/report.py`, then
open `reports/harness/recover.html`. It explains each stop, the observed recovery
decision, the required checks and whether a model ran. Compare the recovered
repair with the conflict example; deliberate stops are labelled separately from
model repair failures. Session evidence opens a drawer with the journal, handoff,
partial patch and per-session results. Saved report evidence survives moving the
report folder; source working copies remain separate.

Reference includes the shared [glossary and file guide (#111)](https://github.com/adamcbrewer/mapbox-tui/issues/111).
Passing checks and readable reports do not mark the learning checkpoint reviewed.

## Recover a subscription-model repair

`recover.py --live` uses the existing local Pi subscription preferences. It keeps
that exact provider, model and limits in the session record; changing local
preferences later applies to new sessions. Resume uses the saved settings.
The existing OpenAI-only scope and subscription authentication remain in place.

A read/search now returns a short `read_id`, such as `r12345678-1`. Live recovery
edits pass that ID instead of copying the 64-character fingerprint. Python looks
up the original file and full hash, then performs the same exact-byte check.
The ID does not mean “whatever is there now”: an older version is refused, an ID
for another file is refused, and an unknown ID cannot authorise a write. Explicit
hashes remain supported by the older prepared tools.

Saved read IDs survive fresh context because they belong to completed journaled
read results. If an edit's reply is lost, recovery resolves the same original
reference and compares the saved before/after hashes before deciding what happened.
The journal, file scope, edit budget and independent checks stay in Python.

Before each model call, Node asks Python to reserve a numbered request. Python
saves that reservation before replying. A lost reply still consumes the slot:
the next fresh context continues with the remaining budget and does not replay
the uncertain network request. Each reservation permits one network dispatch;
provider retries are refused even if an adapter attempts one internally.

Fresh context receives the observed edit count, last completed tool result and
next required tool. After an already-applied edit, it must request a check before
another repair. Approved repository guidance is reloaded from the checked source
copy. The previous conversation isn't resumed or treated as authority.

Using your existing prepared image and local subscription sign-in, from Bash:

```sh
HARNESS_IMAGE_ID="$(cat /home/adam/localhost/mapbox-tui/reports/harness/prepared-image.id)"
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/recover.py \
  --image "$HARNESS_IMAGE_ID" --live --fault after-write
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/recover.py --resume RUN_ID
```

The first command deliberately exits 1 after the model's edit is written. Resume
reconciles it before starting a fresh model conversation. A correct repair must
still pass the independent checks. A failed repair may use the one remaining
repair opportunity; a model saying “done” cannot substitute for those checks.
AI repairs can fail, unlike a prepared demonstration with a known working reply.

Both entries share the original 600-second deadline, twelve tool calls and two
repair attempts. Model requests share the saved configured limit. Each request
has the existing two-minute timeout; the selected output-token preference is
retained. No API-key fallback, automatic transport retry or budget reset is added.
A new reservation is spent even when dispatch or usage couldn't be observed.
The final token summary distinguishes reserved requests, observed dispatches and
missing usage, rather than assuming that an interrupted request was free.

When the model-request limit is reached with a saved edit awaiting verification,
the runner requests the fixed independent check itself. This uses one remaining
tool call and the original deadline; it makes no further model request. A passing
receipt can complete the task without a final model explanation. A failed check
still stops, and cancellation, expired time, exhausted tools, conflicting files
or interrupted verification cannot be bypassed. Other model/transport errors do
not trigger this path. The journal records the runner's check request separately.

Live and prepared run records share the recovery evidence directory. Look for
`candidate_source: AI`, the saved model identity, `model_reservations` and the
per-entry context and logs. The same cancellation, conflict, incomplete-check
and final-publication rules apply. Learning review remains separate from a
passing repair.

## Let Pi retry a failed repair

`pi_run.py --verify` connects Pi's check tool to independent verification.
Every proposed patch is frozen and checked in a fresh copy. A genuine failed
repair permits one revision in the same Pi conversation. Missing tools,
invalid evidence, cancellation and timeouts stop without another repair.

The whole session permits two repairs total, one replacement and check per
repair, 12 tool calls and 600 seconds. Live mode retains the configured model
request limit across both attempts, with the existing two-minute request timeout.
Provider/SDK automatic retries remain disabled. This one repair retry is an
explicit response to a failed independent check, not a transport retry.
Pi cannot change the checks, permissions or budgets. Its final words don't
decide success; the final patch and both attempt receipts must match.
The tests, commands, runner source and check environment are fingerprinted before
the session starts. Both attempts must use those same rules. A rule change stops
the session as `invalid_input`; start a new session to use the changed rules.

From this worktree's root in Fish, using the existing prepared image and installed
Pi package described below and in [the Pi guide](pi/README.md):

```fish
set -l HARNESS_IMAGE_ID (cat /home/adam/localhost/mapbox-tui/reports/harness/prepared-image.id)
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/pi_run.py \
  --image "$HARNESS_IMAGE_ID" --verify --demo retry
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/pi_run.py \
  --image "$HARNESS_IMAGE_ID" --verify --demo exhausted
python3 tools/harness/report.py
xdg-open reports/harness/verify.html
```

These use prepared replies through the real Pi SDK, with no AI calls or model
credentials. Retry should exit 0 as `verified_repair_passed`: the first repair
fails its import test, then the revised exact patch passes all eight required
checks. Exhausted should deliberately exit 1 as `budget_exhausted`: both repairs
fail and no third edit is allowed. Expect 16 and 11 removed containers respectively,
including the initial startup checks. Each demonstration takes several minutes.

Use `--live` instead of `--demo retry` to use the configured OpenAI subscription.
The five local preferences and Pi sign-in in the [Pi guide](pi/README.md) are
required. This new feedback path is tested with prepared replies and the offline
live adapter; this checkpoint does not establish a new AI repair success rate.
Pi still edits only `go/import.go`; adding helper files through Pi is not demonstrated.

Open Session receipt for the shared budgets, context and Pi conversation. Each
Check receipt retains that attempt's exact patch, logs, check identities and
fingerprints. A second edit doesn't overwrite the first attempt's verification
evidence. Files stay local under `reports/harness/repair/runs/<session-id>/` and
`reports/harness/verification/runs/<attempt-id>/`. No model claim can replace a
matching receipt. Human review and the learning checkpoint remain separate.

## Verify one frozen patch

Verify takes the exact bytes of a proposed repair and checks
them in a fresh copy of the pinned source. A fingerprint identifies the patch.
The result also records the original, broken and repaired source fingerprints,
check identities, exit codes, timings and log fingerprints. The report refuses
to show a pass if the patch or required evidence no longer matches.

The verifier makes no model calls. A supplied patch can come from an earlier Pi
run, but its conversation or earlier receipt doesn't decide the new result.
Python controls the checks; the agent cannot edit their runners or expectations.
Execution uses the existing restricted Podman runner, without a host fallback.
After the focused import test, it adds independent GPX, TCX and KML cases, then requires Go formatting and `make test` from
the candidate's repository root. The source and trusted check scripts stay
read-only. Only `/workspace/bin` becomes a temporary writable filesystem for
the build's output; it disappears with the container. Compiler caches and
test-created files use the existing temporary filesystem.
The extra cases use different filenames, coordinates and XML contents. Their
test source stays in the harness, outside Pi's write scope. The verifier copies
it into separate disposable copies of the original, broken and repaired source.
Only that trusted test file may differ from each recorded source tree. It must
pass on the original, fail for the intended reason on the broken seed, then pass
on the candidate. Otherwise a weak new test could give false confidence.
The saved receipt binds the extra test's fingerprint and logs to the exact patch.
The fixed Make command disables VCS discovery in the archived source and records
the pinned commit explicitly. It uses `GOFLAGS=-mod=readonly -p=1 -trimpath` to
keep package compilation serial and reuse matching build-cache entries within
the existing 512 MiB temporary-disk and 1 GiB memory limits. It still runs every
check in the pinned `make test` target.

The existing image already provides Make, Python and Go v1.27.1. This step adds
no packages or install hooks. We consulted the required
[npm security guidance](https://github.com/lirantal/npm-security-best-practices);
Pi's pinned package and disabled install scripts remain as documented separately.

Use the already prepared immutable image from the
[Connect setup](#container-execution-draft), with CPU, memory and pids delegated as
described in the [Pi commands](pi/README.md). From this worktree's root, in Fish:

```fish
set -l HARNESS_IMAGE_ID (cat /home/adam/localhost/mapbox-tui/reports/harness/prepared-image.id)
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/verify.py \
  --image "$HARNESS_IMAGE_ID" --demo new-file
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/verify.py \
  --image "$HARNESS_IMAGE_ID" --demo forbidden-file
python3 tools/harness/report.py
xdg-open reports/harness/verify.html
```

The image-ID path above points at the existing main-checkout evidence on this
machine. Use your own recorded image-ID path elsewhere. No image is pulled or
rebuilt by these commands.

New-file should return `repository_verification_passed` (exit 0). It repairs the
importer using the explicitly permitted new `go/import_bom.go` helper. Both files
belong to the frozen patch and its source fingerprints. All eight checks must
pass, with nine containers removed.
Forbidden-file should return `invalid_input` (exit 1). It deliberately tries to
add a helper at the protected test path instead. The verifier refuses it after
the original/seed checks, before executing any candidate code. Three containers
should be removed. This is a successful refusal, not a failed import test.

`--demo good` retains the earlier existing-file repair:
Good should return `repository_verification_passed` (exit 0): original passes,
planted bug fails, repair passes, then the extra cases confirm those same three
outcomes before formatting and repository tests pass.
`--demo tailored` should return `repair_failed` (exit 1): the repair recognises the original
three filenames and passes their test. The extra cases expose that shortcut.
Formatting and the repository suite stay unrun. This failure is deliberate.
Good should confirm removal of nine containers; tailored, seven.
`--demo unformatted` retains the earlier formatting failure:
Unformatted should return `repair_failed` (exit 1): its repair passes the import
and extra cases but fails formatting. The repository check stays unrun. This
failure is deliberate and confirms removal of eight containers.
`--demo wrong` retains the earlier ineffective repair, which fails at the import
test and uses four containers.

Use `--patch PATH` instead of `--demo` to check a saved candidate patch against
the task's seeded source. It may edit `go/import.go` and add only the new
`go/import_bom.go` named in the trusted task's `allowed_new_files`. No wildcards
or command-line permission overrides. New-file permissions accept plain,
non-test Go source paths directly under `go/`. Links, executable files, deletions,
renames, mode changes, and edits to tests/check scripts remain refused.
Both Git diffs and plain unified patches can describe a permitted addition.
`result.json` lists each changed file's before/after fingerprints; a new file has
no before fingerprint. Its complete contents are also in `candidate.patch`.
Pi's existing edit tool still edits the importer only. These new-file examples
use supplied patches; model-created helper files are not demonstrated here.
`--demo empty` demonstrates refusing an empty repair before starting Podman.

Evidence stays local under `reports/harness/verification/runs/<run-id>/`.
Open Receipt in the Verify report, then inspect Result, Events and Candidate
patch. Command output stays beside the receipt in `*.stdout` and `*.stderr`.
Saved independent-test source uses `.go.txt`, so Go's repository-wide test search
doesn't treat evidence as application code. Earlier local copies were renamed
without changing their contents, then the Verify report was recaptured. Historical
independent-case receipts still recognise their unchanged test fingerprints.
Check records are saved while the run progresses, so an interrupted run retains
its observed steps. Crash recovery is still #106; this isn't a durable journal.

The current `frozen-evaluator-v1` scope requires eight checks: the three original
import checks, the three extra-case checks, formatting and repository tests.
It also binds each check and both attempts to the rules recorded at startup.
Older `focused-only-v1`, `repository-checks-v1`, `independent-cases-v1` and
`permitted-files-v1` receipts retain their original scope. They do not establish
that verification rules were frozen for the whole session and cannot satisfy
the current gate.
Missing checks, mismatched source fingerprints or changed logs
prevent a pass. Environment failures stay separate from failed repairs.

The bounded Pi repair retry is described above. The harness's own tests run separately when
developing this tool. `fully_verified` stays false. Human review remains pending.

I'm building a tool that can take a bug, let an AI try fixing it, then check the
change before I review it. I also want to understand how it works, so we're
building it in small steps.

The prepared calibration below started in [#103](https://github.com/adamcbrewer/mapbox-tui/issues/103),
with reporting tracked in [#111](https://github.com/adamcbrewer/mapbox-tui/issues/111).
Those examples use no AI. They check that our scripts distinguish a working
repair from an ineffective one, using known answers.

## What happens

The script copies a specific Git commit of mapbox-tui into a temporary folder:

1. Test the original working code. It should pass.
2. Deliberately remove the code that handles a UTF-8 marker at the start of an
   imported file. The same test should now fail for the expected reason.
3. Apply a prepared repair and run the test again.

The good repair puts the removed code back. The empty example changes nothing.
The wrong example changes something that doesn't fix the bug. Those last two
should still fail. That's how we check that doing nothing, or just changing some
code, can't count as fixing the problem.

Your working files stay untouched. The copy comes from the recorded commit, so
it doesn't include uncommitted edits. Tests run with test caching off. The
script removes the temporary source copy when it exits normally.

## Try it

This pilot runs checks on Linux only. On other systems, the runner saves a
`blocked` result before launching any commands. Windows and macOS execution
support can come later; the generated HTML report can still be opened there.

You'll need Python 3.11+, Git and Go 1.27.1, with the project's Go dependencies
already cached. Run these from the repository root. No Python packages or Node
installs are needed. This tool isn't part of the shipped binary or release archive.

```sh
python3 tools/harness/report.py
python3 tools/harness/run.py --demo good
python3 tools/harness/run.py --demo empty
python3 tools/harness/run.py --demo wrong
python3 tools/harness/report.py
```

Expect `calibration_passed` and exit code 0 for good. Empty and wrong should
return `repair_failed` and exit code 1. Those failures are intentional.

Open `reports/harness/index.html` in your browser. The latest good and wrong
examples show their three test steps first. Older experiments stay under
"Other experiments", including failed and blocked runs. The glossary and file
guide are under "Reference, when you need it".
Expand an experiment's details for its explanation, technical status and diagnostics.
Receipt links open an evidence drawer without leaving the report. Use its File
selector to inspect the result, task, events, candidate patch or seed patch.
JSON is formatted for reading, events remain in order, and patch additions/removals
are coloured. Close or Escape returns you to the run. Empty files and files not
recorded for that run are labelled separately. The short IDs identify run folders.

The report works offline and follows your browser's light or dark preference.
It isn't live. Run `report.py` again after new experiments, then refresh the page.
That script saves the current stage's progress and evidence, then rebuilds its
HTML page. It doesn't run tests.

### Keep a report for each stage

`report.py` uses the stage recorded in `progress.json` to name the report:
`define.html`, `connect.html`, and later `verify.html`, `recover.html`,
`compare.html` and `use.html`. `index.html` is a copy of the latest current-stage
page. Links at the top switch between saved stages.

```sh
python3 tools/harness/report.py
xdg-open reports/harness/define.html
xdg-open reports/harness/connect.html
```

Rebuilding Connect leaves Define unchanged. Each stage keeps its own progress,
provenance and copied receipts/logs under `reports/harness/snapshots/<stage>/`.
Each HTML file embeds the result, task, events and patches, plus Pi context and
conversation where recorded, so the drawer also works
when you open the HTML directly from disk or copy it elsewhere on its own.
Copy both stage pages to keep their navigation links working. Include the
`snapshots/` folder when you also want original-file links and the remaining logs.
The source archive (`snapshot.stdout`) and command logs aren't embedded.
All generated files remain local and Git-ignored.

Captures are built in a temporary sibling directory. Updating an existing stage
uses Linux [renameat2 exchange](https://man7.org/linux/man-pages/man2/rename.2.html)
to swap the complete snapshot into place. If copying, manifest writing or the
exchange fails, the previous snapshot stays intact. The filesystem must support
this exchange; there's no partial-update fallback.

The Define report was restored from the progress record at commit `6754d3e` and
the original #103 evidence in the main checkout. It uses the current design and
is labelled historical. Its recorded review status is retained; #103 is now
closed. The original evidence wasn't rerun or changed. Define explains the host
checker; the later Podman demonstrations appear in Connect.

To apply a future design update to saved pages without recapturing their data:

```sh
python3 tools/harness/report.py --refresh-saved
```

Refresh checks the saved progress, receipt inventory and every copied evidence
file, including patches, conversations and logs. Changed, missing or added files
stop regeneration. Symlink targets are recorded without reading through them.
Older snapshots without this full inventory need explicit verification against
their original evidence before recording an inventory; refresh never invents
that baseline.

Historical restoration accepts `--restore-from COMMIT --evidence DIRECTORY`.
It requires the original receipts and refuses to replace an existing snapshot.
Git contains the old progress/code, but the ignored run evidence must also have
survived. Restoration never changes either checkout's tracked files.

In the agent environment, prefix runner commands with `mise exec go@1.27.1 --`.
The commands above assume Go is already selected. Checks don't download
dependencies, so missing cached dependencies produce `blocked`. Check the logs
for the cause.

## Try the setup failures

These examples use the prepared good repair, but deliberately stop us from
checking it. They should all exit with code 1:

```sh
python3 tools/harness/run.py --demo good --fault missing-source
python3 tools/harness/run.py --demo good --fault missing-dependency
python3 tools/harness/run.py --demo good --fault timeout
python3 tools/harness/report.py
```

- `missing-source` removes the importer only from the temporary copy. Expect
  `blocked`, with the missing file named in the receipt.
- `missing-dependency` uses an empty temporary Go dependency cache, with downloads
  disabled. Expect `blocked`, with compiler diagnostics in `baseline.stdout`
  and `baseline.stderr`.
  Your normal cache stays untouched.
- `timeout` runs a Python command that waits for 60 seconds, but gives it only
  one second. Expect `timeout`, with partial logs in `timeout-probe.stdout`.
  This exercises the same command deadline used for tests, without relying on
  how fast your machine runs a test. No import test runs in this example.

`--fault` records the requested problem separately from the proposed repair.
The receipt also records when that problem was actually introduced. If setup
stops earlier, the report won't claim we tried the requested failure example.
These outcomes don't mean the repair is bad. They mean we couldn't judge it.
The script must still report what actually happened if another problem stops
the run first, such as Go being unavailable.

## Instructions and patches

`task.json` is the instruction sheet: starting commit, expected behaviour,
permitted file, required test and ten-minute limit. It records hashes too, which
are fingerprints of the source and test files. The existing Go test creates its
example inputs from prepared strings in the two recorded test-source files.
It doesn't use personal activity data.

- **Baseline:** the original code, before we deliberately break it.
- **Seed patch:** the change that plants the bug in the temporary copy.
- **Candidate patch:** the proposed repair we're testing.

A patch lists code lines to add or remove. To try your own **trusted** repair,
write it against the deliberately broken version and run:

```sh
python3 tools/harness/run.py --patch /path/to/repair.patch
```

Only edits to the existing `go/import.go` are allowed. Changes to tests, unrelated
files, file modes or paths outside that scope are rejected. Git reads the patch
metadata for this check, so words inside source strings don't count as file operations.

That restriction doesn't make arbitrary code safe to run. Tests still run as
your user and inherit your environment. Only use trusted patches you've reviewed.
We've chosen rootless Podman for execution isolation in
[#104](https://github.com/adamcbrewer/mapbox-tui/issues/104). The container execution
path has passed the trusted live demonstrations. Pi stays outside with its model connection; our runner
applies allowed changes and runs checks inside a disposable container. The container
gets a project copy, prepared dependencies and resource/time limits, without
personal files, credentials, the real checkout, a container-engine socket or
network access during tests. Bubblewrap isn't needed for this first version.
The separate probes demonstrated these restrictions before the live model runs. See the
[Podman documentation](https://docs.podman.io/en/latest/markdown/podman.1.html).

Pi lets the AI use the controlled tools to investigate and attempt a repair. The
Python checker stays outside that loop as a separate process. Pi's developer
package follows the required
[npm security guidance](https://github.com/lirantal/npm-security-best-practices),
including pinned dependencies and disabled lifecycle scripts.

## Check the planned container runtime

From this worktree's root:

```sh
python3 tools/harness/sandbox.py
python3 tools/harness/report.py
xdg-open reports/harness/index.html
```

The readiness command asks local Podman for its runtime description. It requires
Linux, a normal user, rootless operation, cgroups v2 with CPU/memory/process
controllers, and seccomp support. It forces local inspection even if Podman is
configured to use a remote connection. The shared runner gives the inspection
30 seconds, saves partial logs and handles Ctrl+C/SIGTERM.

Expect `runtime_ready` and exit 0 only when those prerequisites are reported.
A missing or unsuitable runtime returns `blocked` and exit 1. A timeout or
cancellation keeps that separate outcome. The report explains the result under
"Runtime readiness and setup history" and links to its receipt.

Without `--image`, this checks prerequisites only. It does not start a container, build or pull an
image, run a candidate, or make the existing host calibration command safe for
AI-written changes. Runtime reports alone don't prove access restrictions or
resource enforcement. Those observations come from the separate container and
probe receipts below. This readiness command doesn't connect Pi.

The initial check on 22 October 2026 stopped because `podman` was missing.
Installing the system package needs administrator access (`sudo` requires a
password). No system package or container configuration was changed. Docker being
available does not satisfy the agreed rootless Podman prerequisite.

After Podman was installed, the app session reported only `memory` and `pids`,
even though the user manager already delegated `cpu` too. This is a per-session
controller difference, not a missing package or a reason to drop the CPU limit.
Launching the harness in a temporary systemd user unit reports all three:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/sandbox.py
```

Run this without sudo. The same prefix can launch container calibration after
image preparation. The unit is transient; no system-wide delegation settings
were changed. See [systemd-run](https://www.freedesktop.org/software/systemd/man/latest/systemd-run.html).

Review the successful decision path and deliberate rejection paths with:

```sh
python3 -m unittest discover -s tools/harness -p 'test_sandbox.py' -v
```

These tests supply prepared runtime descriptions, including rootful, remote,
incomplete and missing-controller examples. They prove the decision and receipt
handling, not live container isolation. The missing-runtime test proves there is
no fallback command. Existing harness tests cover the shared command deadline
and process cancellation behaviour.

Readiness receipts and command logs stay under
`reports/harness/sandbox-preflight/runs/<run-id>/`, separate from import repair
measurements. `runtime_ready` is not a verified repair or human approval.

The prerequisites follow the [Podman info documentation](https://docs.podman.io/en/latest/markdown/podman-info.1.html)
and [rootless resource-limit documentation](https://docs.podman.io/en/latest/markdown/podman-run.1.html).
We also consulted the required [npm security guidance](https://github.com/lirantal/npm-security-best-practices).
Readiness itself adds no package dependencies. Pi now has its own separate
[developer package](pi/README.md).

## Container execution draft

This is the next #104 slice. Unit tests exercise its policy and cleanup paths.
The first live checkpoint passed using Podman v6.1.4, a pinned Go image and
trusted prepared examples. Review this checkpoint before connecting Pi.

The runner requires a complete local image ID, such as the value Podman writes
to an `--iidfile`. Tags such as `latest` are rejected. Runtime checks never pull
images or download Go modules. The image must contain Go v1.27.1 at
`/usr/local/go/bin/go`, plus the task's prepared dependencies in `/go/pkg/mod`.

Image preparation is separate and uses the network. The following recipe
uses the initial verified Linux/amd64 Go base image.
It copies only the pinned task's public module files into a temporary build
context, with no personal files or credentials:

```sh
HARNESS_GO_BASE='docker.io/library/golang@sha256:e114385c58e7280d2f56d6cf374219dda33fef8eb77f72e9f290ccae6160bd90'
HARNESS_BASE_COMMIT="$(python3 -c 'import json; print(json.load(open("tools/harness/task.json"))["base_commit"])')"
HARNESS_IMAGE_CONTEXT="$(mktemp -d)"
git show "$HARNESS_BASE_COMMIT:go.mod" > "$HARNESS_IMAGE_CONTEXT/go.mod"
git show "$HARNESS_BASE_COMMIT:go.sum" > "$HARNESS_IMAGE_CONTEXT/go.sum"
cp tools/harness/Containerfile "$HARNESS_IMAGE_CONTEXT/Containerfile"
mkdir -p reports/harness
podman pull "$HARNESS_GO_BASE"
podman build --pull=never --build-arg GO_IMAGE="$HARNESS_GO_BASE" \
  --iidfile reports/harness/prepared-image.id "$HARNESS_IMAGE_CONTEXT"
rm -r -- "$HARNESS_IMAGE_CONTEXT"
```

The initial image was built locally; its base reference is recorded in
`reports/harness/base-image-selection.json`. The Containerfile checks the Go version,
downloads the module cache and verifies it before any candidate is supplied.

After image preparation, these are the live repair checks:

```sh
HARNESS_IMAGE_ID="$(cat reports/harness/prepared-image.id)"
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/sandbox.py --image "$HARNESS_IMAGE_ID" --demo good
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/sandbox.py --image "$HARNESS_IMAGE_ID" --demo wrong
python3 tools/harness/report.py
```

Good should exit 0 with `calibration_passed`; wrong should exit 1 with
`repair_failed`. Neither result is human approval or proof of full isolation.
The existing baseline/seed/candidate assertions are reused. Git archive and
bounded patch operations stay in the trusted host runner; Go version and all
three test stages execute in separate containers. There is no host Go fallback.

Each container has no network, a read-only root and source copy, a 512 MiB
temporary filesystem, two CPUs, 1 GiB of memory with no extra swap, and at most
256 processes. It uses an unmapped normal user, no capabilities, the recorded
default seccomp profile, no image health checks and an empty OCI hook directory.
Only a fixed environment is supplied. The real checkout, reports, home directory
and engine socket aren't mounted. Configuration is inspected before startup.

After the check, the runner inspects the exit state, treats a recorded memory
exhaustion as a blocker, then force-removes its uniquely named container and
checks that it is absent. Cleanup gets a separate ten-second budget even after
the task deadline or cancellation. Unconfirmed removal prevents success. A hard
process kill can still interrupt cleanup; recovery remains #106.

The receipt includes the image ID, runtime/profile identities and each container's
removal state. Unit tests use prepared Podman responses; live evidence is retained
separately. The first live good repair passed, the wrong repair failed as intended,
and all eight containers were confirmed removed.

### Live boundary and cleanup demonstrations

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/probes.py --image "$HARNESS_IMAGE_ID" --phase boundary
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/probes.py --image "$HARNESS_IMAGE_ID" --phase timeout
```

Boundary should exit 0 with `boundary_passed`. The runner first proves a dummy
file and HTTP endpoint are readable on the host. The trusted probe then confirms
both are unavailable inside the container, root/source writes fail, privileges
are restricted and kernel cgroup files contain the requested resource limits.
This reads the applied kernel limits; it doesn't stress-test every limit.

Timeout deliberately starts a sixty-second command with a five-second budget.
Expect `timeout` and exit 1, with `probe_confirmed: true` only after readiness
output was observed and container removal was confirmed.

To review cancellation, start this in one terminal:

```sh
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  --unit=mapbox-harness-cancel-review -p 'Delegate=cpu memory pids' \
  python3 tools/harness/probes.py --image "$HARNESS_IMAGE_ID" --phase cancel
```

After the probe's `cancel-probe.stdout` contains `probe-ready`, use another terminal:

```sh
systemctl --user kill --signal=SIGTERM --kill-whom=main mapbox-harness-cancel-review.service
```

Expect `cancelled` and exit 1, with readiness and removal confirmed in its receipt.
The automated walkthrough already exercised this sequence. Killing only the
`systemd-run` client is not the cancellation demonstration.

Probe receipts live under `reports/harness/sandbox-probes/runs/`. The report
explains each demonstration and links to its receipt. These probes use dummy
data and no model. They establish observed behaviour for this image/environment,
not a general security proof. The next step is discussing this checkpoint.

References: [Podman create](https://docs.podman.io/en/latest/markdown/podman-create.1.html),
[container inspection](https://docs.podman.io/en/latest/markdown/podman-container-inspect.1.html),
[container removal](https://docs.podman.io/en/latest/markdown/podman-rm.1.html), and
the required [npm security guidance](https://github.com/lirantal/npm-security-best-practices).
The container implementation itself uses Python; the Pi checkpoint has its own
separate Node dependencies.

## What gets saved

Each run gets a folder under `reports/harness/runs/<run-id>/`. The records use
format version 1. Depending on how far the run got, you'll find:

- `task.json`: the instructions used for this run.
- `seed.patch` and `candidate.patch`: the changes used, with SHA-256 fingerprints
  recorded in the instructions or result.
- `result.json`: the outcome, timings, checks, environment and identities of the
  task, source and runner. This is the receipt. Fault examples also record the
  requested fault and, once introduced, `fault_injected`. Missing source/dependency
  results name the blocker.
- `events.jsonl`: a chronological diary, one event per line, with unique IDs and
  UTC timestamps.
- `*.stdout` and `*.stderr`: command output and diagnostics. `snapshot.stdout`
  is an exception: it contains the source archive, not readable text.

The receipt starts as `incomplete`. When the run finishes, the script replaces
it in one operation with the final result. Ctrl+C, SIGTERM and timeouts stop the
current command and its process group, keeping partial logs. Once checks have
stopped and the outcome is decided, further cancellation signals are ignored
while the script finishes saving that result and its final event.
A crash, SIGKILL or machine
shutdown may leave an incomplete record. Recovery comes in #106; this pilot
has no automatic retries or guarantee of crash-safe records.

The outcome codes distinguish `calibration_passed`, `repair_failed`,
`calibration_failed`, `blocked`, `invalid_input`, `timeout`, `cancelled` and
`incomplete`. The report explains them in plain English.

Individual test results use `passed`, `assertion_failed`, `build_failed` or `blocked`. A command
exiting successfully isn't enough: the named test and all three expected
subtests must pass. A repair that introduces a compiler error is `repair_failed`.
Build failures before the repair and missing dependencies are blockers. None
of those failures proves we've reproduced the intended bug.
Tools that exist but cannot start (such as a missing execute permission or an
invalid executable format) also produce `blocked`. An unreadable patch file
remains `invalid_input`.
Setup problems such as a full temporary filesystem produce `blocked`, provided
the report directory can still be written. If evidence can't be saved either,
the runner may exit with an incomplete record rather than a final receipt.
The HTML report only describes a successful repair when its receipt also records
the original code passing, the deliberate bug failing and the repaired code passing.
This catches inconsistent records; it doesn't authenticate them or rerun tests.

Each run tries one candidate, with no repair retries. For the host calibration
command, model and Pi fields are null and model cost is zero because neither is
used. Pi runs record their model and token usage separately. Execution cost is unknown.
Human acceptance stays pending regardless of the test result.

All generated files stay local in the Git-ignored `reports/` directory. No commit,
upload, public export or storage service needed per run. Deleting those files
loses that local history.

`progress.json` separately records our decisions and review status: planned,
active, blocked, awaiting-review or reviewed. Tests don't change that status.
The saved Define snapshot records #103's historical checkpoint. Connect now
contains prepared examples, sandbox observations and live Pi attempts. Later
stages and run timelines aren't built yet. Runtime-readiness records stay separate.

## Check the implementation

```sh
python3 -m unittest discover -s tools/harness -p 'test_*.py'
node --test tools/harness/pi/loop.test.mjs tools/harness/pi/native.test.mjs
python3 tools/harness/pi_integration_test.py
make test
```

The Python tests check the harness itself: misleading completion text, missing
tests or tools, attempts to edit the tests, timeouts, incomplete evidence and
report handling, including escaping text safely. The repository checks cover the
existing product. Run the repair demos and setup-failure examples above to see
the actual outcomes, including why an unfinished check is not a failed repair.
The plain Python suite needs no Node packages. CI runs the SDK tests separately,
using Node v26.12.1 and the locked Pi dependencies with install scripts disabled
and a seven-day npm release cooldown. This follows the
[npm security guidance](https://github.com/lirantal/npm-security-best-practices).
The Python-to-Pi integration test uses prepared replies and mocked check execution;
it needs no account, model calls or Podman. CI does not run the container demos
or upload local records.

Before moving on, I want to be able to explain why the deliberately broken code
failed, why the good repair passed, and what could still be wrong despite that
pass. This only checks one import behaviour. It doesn't prove that a whole patch
is correct or ready to merge.
