# Developer harness pilot

I'm building a tool that can take a bug, let an AI try fixing it, then check the
change before I review it. I also want to understand how it works, so we're
building it in small steps.

This is the first part of [#103](https://github.com/adamcbrewer/mapbox-tui/issues/103),
with the local report from [#111](https://github.com/adamcbrewer/mapbox-tui/issues/111).
There's no AI involved yet. We're checking that our scripts can tell a working
repair from an ineffective one, using examples where we already know the answer.

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

Open `reports/harness/index.html` in your browser. The report explains each
experiment, with a glossary and file guide under "Words and files explained".
Expand an experiment's details for the technical status and diagnostics. Receipt
links open the saved JSON results; the short IDs help you find their folders.

The report works offline and follows your browser's light or dark preference.
It isn't live. Run `report.py` again after new experiments, then refresh the page.
That script only rebuilds the report; it doesn't run tests.

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
[#104](https://github.com/adamcbrewer/mapbox-tui/issues/104). It isn't implemented
in this pilot. Pi will stay outside with its model connection; our runner will
apply allowed changes and run checks inside a disposable container. The container
will get a project copy, prepared dependencies and resource/time limits, without
personal files, credentials, the real checkout, a container-engine socket or
network access during tests. Bubblewrap isn't needed for this first version.
We'll demonstrate the restrictions before executing AI-written changes. See the
[Podman documentation](https://docs.podman.io/en/latest/markdown/podman.1.html).

Later, Pi will let the AI use tools to investigate and attempt a repair. This
checker can stay outside that loop as a separate process. Before adding Pi's Node
package, we'll review the required
[npm security guidance](https://github.com/lirantal/npm-security-best-practices),
including pinned dependencies and disabled lifecycle scripts.

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

Each run tries one candidate, with no repair retries. Model and Pi fields are
null, and model cost is zero because neither is used. Execution cost is unknown.
Human acceptance stays pending regardless of the test result.

All generated files stay local in the Git-ignored `reports/` directory. No commit,
upload, public export or storage service needed per run. Deleting those files
loses that local history.

`progress.json` separately records our decisions and review status: planned,
active, blocked, awaiting-review or reviewed. Tests don't change that status.
The page covers Define; later stage history and run timelines aren't built yet.

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
