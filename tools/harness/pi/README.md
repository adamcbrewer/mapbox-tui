# Pi harness (#104)

I want Pi to investigate a bug and try fixing it, while our runner controls which
files it can change and how tests run. This step connects Pi's SDK to our Python
runner through four tools: read, search, edit and check.

Prepared replies test the connection without using a model. The live mode lets
my selected subscription model choose its own investigation and repair steps,
with the same four tools and sandboxed check.

Before Pi starts, Python checks the recorded source commit and container image,
tests the original code, then confirms that our planted bug fails as expected.
Pi can read and search three approved files, edit `go/import.go` once using its
current content hash, and request one fixed import test. The test runs in our
existing rootless Podman sandbox.

We've turned off Pi's default tools, shell access, extensions, skills, MCP and
automatic instruction loading. Retries, context compaction and cache warming
are also off. Prepared examples follow a supplied sequence; the live model
chooses its own calls.

Python supplies the task's approved `AGENTS.md` from the pinned source snapshot.
Its recorded hash must match before Pi starts. The effective text and hash are
saved in Context; local or nested instruction files aren't discovered. Repository
guidance applies within the four tools and fixed check allowed for this task.
Earlier saved runs predate this addition and retain their original context.

The scripted Pi process runs in Node with a temporary home and no credentials in
its environment. It can access this developer package and its temporary folder;
Node permissions block network access and starting other processes. Python
remains trusted code on the host and owns Podman. Those restrictions apply to
the scripted Pi process and project execution, not arbitrary changes to Python's
own code.

## Install or restore the package

You'll need Linux, Python 3.11+, Node 26.7+, npm 11.10+, and the prepared rootless
Podman environment from [the harness README](../README.md). This is developer
tooling and stays separate from the app and its releases. The Node version also
needs to support the permission flags we use.

From the worktree root, in Fish:

```fish
cd tools/harness/pi
npm ci --ignore-scripts --no-audit --no-fund
cd ../../..
```

Pi is pinned to v1.2.0, with its dependencies recorded in the lockfile. On
22 October 2026, that was the newest release allowed by this machine's existing
seven-day npm cooldown. We left the global npm settings alone.

Install scripts are disabled, direct dependency versions are pinned, and Git
dependencies are refused. These choices follow the required
[npm security guidance](https://github.com/lirantal/npm-security-best-practices).
Use `npm ci` to restore the recorded dependency versions.

## Try a working repair and one that should fail

From the worktree root, in Fish:

```fish
set -l HARNESS_IMAGE_ID (cat reports/harness/prepared-image.id)
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/pi_run.py \
  --image "$HARNESS_IMAGE_ID" --demo good
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/pi_run.py \
  --image "$HARNESS_IMAGE_ID" --demo wrong
python3 tools/harness/report.py
xdg-open reports/harness/connect.html
```

The good example should return `scripted_loop_passed` (exit 0). The wrong example
should return `repair_failed` (exit 1). That failure is useful: it shows that an
ineffective change doesn't get counted as a repair.

Each example makes six requests: read, search, try reading a host file, edit,
repeat the edit, then check. The host-file read should be refused. Repeating the
same edit should return its earlier result without changing the file again.
Both examples should confirm that all four containers were removed.

There are limits: 12 tool calls, one repair, one check and 600 seconds including
startup. No model requests are made. A timeout or cancellation stops the owned
Pi process, removes any active container and confirms that it's gone. Cleanup
gets a separate ten seconds, even after the main deadline.

Remembering a repeated edit only works within this run. Durable recovery after
a crash, including SIGKILL, is still #106.

In the report, open **Pi tools**, then a Receipt:

- **Events** shows the refused read and repeated edit.
- **Candidate patch** shows the change we tested.
- **Context** shows the actual prompt, tool definitions, task and Node permissions.
- **Pi conversation** shows the prepared requests and the runner's replies.

The evidence stays local and ignored by Git, under `reports/harness/pi/runs/`.

What I want to understand from this step is who controls what. The model will
choose actions; our runner decides whether they're allowed. We also need to
tell an ineffective repair apart from a setup problem, malformed reply or
unfinished run. Passing this demonstration doesn't finish #104 or prove model
access through a subscription.

## Choose a subscription and model

Both choices go in `config.local.json`. It's ignored by Git, and
`config.example.json` is the template. This file holds preferences; credentials
live separately.

For this pilot, I only want OpenAI support. Use `"provider": "openai"` with a
Codex/ChatGPT subscription. Claude is outside the current scope. Other provider
settings are rejected before sign-in or model work.

The exact OpenAI model remains configurable. To list Pi's models, leave `model`
as `null` and run `status` below. We don't substitute a different model. Pi's
[provider guide](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/providers.md)
describes its sign-in support.

```fish
node tools/harness/pi/setup.mjs status
node tools/harness/pi/setup.mjs login
```

Run login in your own terminal and follow the browser instructions. Pi needs its
own sign-in; we don't copy Codex's token file. Setup makes no model requests.
The model list comes from Pi's catalogue, so seeing a model there doesn't tell
us whether the account can use it.
Setup only reports live readiness when the exact selected model exists in that
catalogue and uses the supported OpenAI Responses transport, sign-in is present
and the request limits are valid. A typo or unsupported model gets a blocker
instead of instructions to run it. This check makes no model request and doesn't
prove account access.

OpenAI's [sign-in flow](https://developers.openai.com/siwc/token-sharing-open-source/sign-in)
requires a stable ID for this installation. We reuse Pi's ID from
`~/.pi/agent/settings.json`, or create and save one through Pi's settings manager.
Existing settings are preserved. If they're damaged, login stops rather than
resetting the identity. This only changes Pi's user settings and needs no
administrator or system configuration changes.

The app introduces itself as "Mapbox TUI harness". Known login failures get a
useful explanation without printing private token responses. Credentials stay
in `~/.pi/agent/auth.json`, outside the repository, reports, prompts and
containers. Keep that file private and don't paste its contents into chat.

My local defaults are `gpt-6-luna`, ten model requests and no hard output-token
cap (`maxOutputTokensPerRequest: null`). The fields are `model`, `maxModelRequests` and
`maxOutputTokensPerRequest`. Auth must be `"subscription"`; API-key mode is
rejected.

## Run one subscription repair attempt

After signing in, from the worktree root in Fish:

```fish
set -l HARNESS_IMAGE_ID (cat reports/harness/prepared-image.id)
systemd-run --user --quiet --wait --pipe --collect --same-dir \
  -p 'Delegate=cpu memory pids' python3 tools/harness/pi_run.py \
  --image "$HARNESS_IMAGE_ID" --live
python3 tools/harness/report.py
xdg-open reports/harness/connect.html
```

I've chosen to run this subscription pilot without a hard token cap, keeping the
request and timeout limits. OpenAI's
[subscription preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)
require us to omit `max_output_tokens`. A numeric cap still stops the run when
the subscription transport removes it; `null` explicitly accepts provider defaults.

This uses subscription allowance. It doesn't switch to API-key billing. The
exact provider, model, low reasoning effort and configured limits go in the
receipt. The live Pi process can use the network and its saved Pi auth directory
for sign-in and token refresh. It still cannot launch processes or access the
project copy directly. Only Python's four tools supply source or apply changes.
The disposable project's tests still have no network or credentials.

There are at most ten model requests, each with a two-minute HTTP timeout.
The full run has a ten-minute deadline including startup, plus the existing
cleanup allowance. These limits aren't a total-token or cash cap, and stopping
a client doesn't guarantee a provider immediately stops generation.
The two-minute timer covers the whole response stream, including a provider
that sends headers and then stops replying. Container check logs are capped at
2 MiB combined stdout/stderr; exceeding that stops the check and triggers cleanup.
Podman's duplicate on-disk container logging is disabled.
We also enforce 12 tool calls, one edit, one check and the 600-second deadline.
The actual provider payload is checked before sending; saving a preference alone
isn't proof that a provider enforces it. Model and provider retries,
compaction and cache warming are off. A partial response at the output limit
cannot execute tool calls. The model isn't given the prepared good patch.

Expect `live_repair_passed` only if the model edits the permitted file, the fixed
check passes and the loop finishes. An ineffective edit returns `repair_failed`.
A provider problem, exhausted budget or unfinished loop stays separate from a
passed check. The run can fail; we don't increase limits or retry automatically.

Open the report's Pi tools section. The live card names the model and usage.
Use its Receipt drawer to compare Context, Pi conversation, Candidate patch and
Events. Requests, stop reasons and provider-reported usage are saved locally;
`token_summary` adds the recorded totals and largest response. Input includes
cached input; output includes reasoning. Those are subsets, not extra tokens.
Inputs can repeat conversation history across requests, so a run's total input
is larger than the initial prompt. Actual subscription charges aren't known.
Unknown usage after a failed request mustn't be read as proof that no allowance was used.

This is still the planted regression used for calibration. One successful model
repair doesn't finish #104 or establish how it handles unfamiliar real bugs.
We stop here for review before moving to independent verification in #105.

The pinned package and disabled install scripts remain as described in the
[npm security guidance](https://github.com/lirantal/npm-security-best-practices).
The [Luna model documentation](https://developers.openai.com/api/docs/models/gpt-6-luna)
describes the selected model. The pilot uses low reasoning effort. OpenAI's
[reasoning guide](https://developers.openai.com/api/docs/guides/reasoning) explains
that the output cap includes reasoning tokens.

## First Luna result

The first uncapped run made five model requests and six tool calls in 96 seconds.
Luna proposed one edit, but all three required import cases still failed. It
reported that failure correctly. All four containers were removed. That's a
failed repair with a completed harness run, not a setup failure or a pass.

Provider receipts recorded 51,284 input tokens and 329 output tokens, 51,613 in
total. No cached input or reasoning tokens were reported. The largest response
was 172 output tokens. Most usage came from resending source and conversation
history on later turns. This one attempt doesn't predict future usage or prove
that 172 tokens would be a suitable cap.

In the report, compare the live candidate patch and conversation with the
prepared working and wrong repairs. The original run is saved locally at
`reports/harness/pi/runs/743759ebfff84ae48c2c6ac3236aa7e4/`. I want to review what
it tried and what the test actually showed before another model attempt.

## Second Luna result

I asked for another attempt with the same limits. The only prompt change asked
Luna to trace the failing input through the source and state the cause before
editing. The prepared repair wasn't supplied.

This attempt passed all three required import cases in 99 seconds. It made five
model requests and six tool calls, edited once and removed all four containers.
It used 51,540 input tokens (including 12,288 cached) and 507 output tokens,
52,047 total. The largest response was 255 output tokens. Zero reasoning tokens
were reported. Cached tokens are already included in the input total.

The report's latest live card shows this passing attempt. Expand **Earlier Pi
runs** to inspect the first failed attempt. The new run is saved at
`reports/harness/pi/runs/da117ff76b74443f8928767e8107a585/`. Together the two runs
used 103,660 tokens. Similar timings didn't make input usage disappear.

One failed attempt and one passing attempt don't prove that the extra instruction
caused the improvement. Review both patches and conversations. Passing this fixed
check still leaves independent verification and human approval outstanding.

## Check the implementation

```fish
python3 -m unittest discover -s tools/harness -p 'test_*.py'
node --test tools/harness/pi/loop.test.mjs
make test
make format-check
```

Go 1.27.1 is required. In the agent environment, prefix Go/Make commands with
`mise exec go@1.27.1 --`.

The Python tests check file scope, symlink escapes, protected tests, stale file
hashes, repeated or conflicting edits, budgets, blocked startup and Pi-process
cleanup. The SDK tests exercise Pi's real loop with prepared replies, check the
runner's result format, and reject unsupported billing or configuration.

The prepared examples let us see the full connection from Pi through to the
container. The automated tests cover the individual rules, including the real
subscription adapter omitting a cap and the guard stopping before any fetch.
