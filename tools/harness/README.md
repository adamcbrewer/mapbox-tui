# Developer harness pilot

Current stage: Connect ([#104](https://github.com/adamcbrewer/mapbox-tui/issues/104)).
Pi now connects an OpenAI subscription model to four controlled tools, with
prepared examples for checking the wiring without AI. Two live Luna attempts
are recorded locally: one ineffective repair and one that passed the fixed
import check. See the [Pi checkpoint and Fish commands](pi/README.md).
The Connect learning checkpoint still awaits review; passing the test isn't approval.
The original repair command still runs trusted code on the host.

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
make test
```

The Python tests check the harness itself: misleading completion text, missing
tests or tools, attempts to edit the tests, timeouts, incomplete evidence and
report handling, including escaping text safely. The repository checks cover the
existing product. Run the repair demos and setup-failure examples above to see
the actual outcomes, including why an unfinished check is not a failed repair.
CI runs the Python tests too; it does not run the calibration demos or upload
their local records.

Before moving on, I want to be able to explain why the deliberately broken code
failed, why the good repair passed, and what could still be wrong despite that
pass. This only checks one import behaviour. It doesn't prove that a whole patch
is correct or ready to merge.
