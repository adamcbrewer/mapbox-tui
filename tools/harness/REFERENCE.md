# Harness reference

Open `reports/harness/telemetry.html` for current progress, timelines and comparisons. Open `index.html` for the saved stage report. Start with a run's receipt and exact candidate when reviewing a result; command logs and source archives are for investigating problems.

## Worked example

Original code passes, deliberately broken code fails, then a working proposed repair passes or an ineffective repair still fails. The middle failure is expected: it proves the example exposes the planted bug. A passing repair still needs human review. Pi chooses tools and proposes edits. Our runner owns limits, evidence and checks. The report reads the saved records.

## Glossary

- **Journal:** A durable record of intended edits and observed results. Resume compares it with the actual files and check receipts before deciding what happened.
- **File hash:** A fingerprint of the file bytes. Matching before/after fingerprints help distinguish an unchanged file from an edit that already happened.
- **Operation ID:** A name for one intended edit. Repeating that ID with the same arguments can recover a lost reply; changing its arguments is refused.
- **Recovery handoff:** A short navigation summary rebuilt from trusted observations. It cannot grant permissions or prove that tests passed.
- **Harness:** The software around the AI that controls the task, tools, checks and saved evidence.
- **Model / agent / Pi:** The model is the AI. An agent uses a model and tools to attempt a task. Pi supplies that loop. Prepared responses can test its wiring without calling a model.
- **Checker / verifier:** Code that runs required checks and records their results. It does not understand or approve the code like a person.
- **Calibration:** Trying examples with known answers to see whether our checking process reports them correctly.
- **Baseline:** The starting code used for comparison. Here it works; a real repair task may start with broken code.
- **Seed patch:** A change that deliberately plants a bug in the temporary copy.
- **Candidate patch:** The proposed repair we are testing. Prepared examples use a supplied patch; live Pi runs save the model's edit.
- **Patch:** A file listing code lines to add or remove.
- **Fixture:** Prepared example input used by a test, rather than your personal activity data.
- **Run / attempt / check:** A run is one execution of a task. An attempt is one proposed solution within it. A check is an individual test or validation step. A run can contain several repair attempts, sharing its original limits.
- **Run ID / hash:** A run ID labels its folder; the short ID beside Receipt helps you find it. A hash is a fingerprint identifying exact file contents. They are different things.
- **Receipt:** The saved result summary and references to its evidence. It is not human approval.
- **Frozen patch:** The exact saved repair bytes, identified by a fingerprint. Changing them invalidates their verification evidence.
- **Permitted new file:** A source path named in the trusted task before the repair runs. Its complete added contents belong to the saved patch. Tests and check scripts stay protected.
- **Independent cases:** Extra test inputs supplied by the verifier, outside Pi's editable files. We check that they pass on working code, fail on broken code and pass after the repair.
- **Bounded repair retry:** After a real failed check, Pi gets one chance to revise the repair. Both attempts share the time, request and tool budgets. Missing tools or other setup failures stop the run instead.
- **Fault example:** A deliberately introduced setup problem or slow command. It tests how the runner handles trouble, not whether a repair works.
- **Podman:** The container tool chosen for a disposable test environment in Connect (#104). Readiness inspects its reported features. Separate live demonstrations check dummy access denials, kernel settings and cleanup.
- **Sandbox readiness:** Checking whether the container runtime reports the prerequisites we need. This does not prove that a container blocks access or enforces limits.

## File guide

- `tools/harness/recover.py / recovery.py`: Retains a working copy and durable journal, reconciles interrupted edits, restores shared limits and creates fresh Pi context.
- `reports/harness/recovery/runs/<run-id>/`: Recovery receipts, partial patch, journal and handoff. Per-entry prompts and conversations are retained under pi-sessions/. These are local evidence, outside Pi's edit scope.
- `journal.json / handoff.json / partial.patch`: The recorded intents and observations, the navigation summary and the observed partial change. A summary saying success cannot override files or independent check receipts.
- `recovery-stop.json`: A refused resume when saved state cannot be reconstructed safely. Earlier receipts remain historical evidence rather than being overwritten.
- `tools/harness/run.py`: Conducts the experiment and saves its evidence.
- `tools/harness/sandbox.py`: Checks local rootless Podman prerequisites. With an immutable prepared image ID, it can check a trusted repair in containers. No host execution fallback.
- `tools/harness/container.py`: Creates and inspects restricted containers, runs fixed checks, then removes each container and confirms it is absent.
- `tools/harness/probes.py`: Runs trusted dummy access checks or deliberately slow commands inside the container. These are separate from repair-quality checks.
- `tools/harness/pi_run.py`: Runs prepared replies or the configured subscription through four controlled tools. Python owns the disposable source, limits and sandboxed checks. A live attempt can stop before inference if its limits cannot be enforced.
- `tools/harness/verify.py`: Freezes a supplied patch and checks the pinned source in a fresh copy. Current runs require the original and independent import cases, formatting and repository make test, with no model calls.
- `tools/harness/repair.py`: Connects Pi's check tool to independent verification, allows one retry after a real repair failure, and binds the final verdict to the last exact patch and both attempts' receipts.
- `reports/harness/repair/runs/<run-id>/`: Pi conversation, shared budgets, tool feedback and links to each attempt's separate verification evidence. Prepared examples use the actual Pi SDK without an AI model.
- `tools/harness/fixtures/import_verifier_test.go.txt`: Trusted extra GPX, TCX and KML inputs with different filenames and coordinates. Copied into disposable verification folders, never Pi's editable source.
- `reports/harness/verification/runs/<run-id>/`: Independent check evidence. Result includes the patch fingerprint, source identities, required scope and each check's exit code, timing and log fingerprints. Older focused-only receipts retain their original scope.
- `tools/harness/pi/config.local.json`: Your ignored local provider, model and subscription settings. Keep model choice and request/output limits explicit. Credentials live separately in Pi's private auth file.
- `context.json / pi-session.json`: The prompt, tool definitions and Pi conversation. Check the mode: prepared replies test wiring; live runs use the recorded subscription model.
- `tools/harness/task.json`: The instruction sheet: starting code, expected behaviour, allowed changes and required test.
- `tools/harness/fixtures/*.patch`: The deliberate bug and prepared example repairs.
- `tools/harness/report.py`: Builds this HTML page from saved records. It does not run tests.
- `tools/harness/telemetry.py / measurements.py`: Build the offline progress, timeline and comparison report from existing receipts. Measurements are a versioned reading of the evidence, not a second source of truth.
- `tools/harness/report_visuals.py`: Builds the visible stat summaries, outcome bars and linked runtime charts. It keeps AI attempts separate from prepared examples and uses native SVG without external assets.
- `tools/harness/measurement-schema.json`: Defines the versioned measurement fields and aggregation rules. Missing measurements are null, not zero.
- `reports/harness/telemetry.html`: Open this for project progress, run timelines, comparisons and the shared reference guide. No server is needed.
- `tools/harness/progress.json`: Records our stage, decisions and pending discussion. Test results do not update our approval.
- `tools/harness/test_harness.py`: Tests the Python machinery itself.
- `tools/harness/README.md`: Commands and instructions for using and reviewing this pilot.
- `reports/harness/index.html`: The latest stage report, with links to saved stage pages.
- `reports/harness/define.html / connect.html`: Separate stage reports. Rebuilding Connect leaves the saved Define page unchanged.
- `reports/harness/snapshots/<stage>/`: Saved progress, provenance and copied evidence for one stage. Include this folder for original-file links and logs; the evidence drawer's files are embedded in the HTML.
- `reports/harness/runs/<run-id>/`: Evidence from one experiment. Within it, task.json preserves the instructions, seed.patch preserves the deliberately broken change, and candidate.patch preserves the proposed repair.
- `reports/harness/sandbox-preflight/runs/<run-id>/`: Separate runtime-readiness receipts and Podman inspection logs. These are not repair attempts or proof of container isolation.
- `result.json (inside a run folder)`: The final summary: outcome, timings and exactly what was checked. Saved stage reports open it in an evidence drawer. Telemetry embeds a readable receipt under each run and links to the original file.
- `events.jsonl (inside a run folder)`: A chronological diary, with one event per line: a command started, finished, or a test result was recorded.
- `*.stdout / *.stderr (inside a run folder)`: Command output and diagnostics. Usually only needed to investigate a problem. baseline and seed refer to the original and deliberately broken code; candidate refers to the proposed repair. scope checks allowed files; metadata checks file operations such as renaming or deleting; apply records applying a patch. go-version records the compiler version.
- `snapshot.stdout`: An exception to the readable logs: this contains the archived source copy. snapshot.stderr contains any archive-command diagnostics.
- `reports/harness/*.log`: Checks run on the harness implementation, separate from the individual experiments.
