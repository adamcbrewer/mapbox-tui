# Pi tool wiring checkpoint (#104)

The real Pi SDK now talks to our Python runner through four controlled tools.
The demonstration uses prepared responses and repairs. It checks the connection,
not an AI's ability to fix code. Live model execution is deliberately not enabled
in this checkpoint.

Python verifies the pinned source and image, checks the original code, and
reproduces the planted failure before starting Pi. Pi can read/search three
approved files, edit `go/import.go` once using its current hash, and request one
fixed import test. Test code runs in the existing rootless Podman boundary.
Default tools, shell access, extensions, skills, MCP, auto-loaded instructions,
retries, compaction and cache warming are disabled.

The scripted Node process has a temporary home, no credential environment,
filesystem access restricted to this developer package and its temporary folder,
and Node permissions deny network and child processes. These are extra SDK
restrictions; the existing Podman boundary still isolates all project execution.
The trusted Python process owns Podman and can access the host. This is not a
sandbox for arbitrary changes to the harness itself.

## Install or restore this developer package

Requires Linux, Python 3.11+, Node 26.7+, npm 11.10+, and the prepared rootless
Podman environment described in [the harness README](../README.md).
This package is separate from the app and its releases.

From the worktree root, these commands also work in Fish:

```fish
cd tools/harness/pi
npm ci --ignore-scripts --no-audit --no-fund
cd ../../..
```

Pi is pinned to v1.2.0 with a lockfile. This was the newest release eligible under
this machine's existing seven-day npm cooldown on 22 October 2026. No global
npm settings were changed. The package disables install scripts, pins direct
versions and refuses Git dependencies, following the required
[npm security guidance](https://github.com/lirantal/npm-security-best-practices).
Use `npm ci` to preserve the recorded dependency tree. The Node version requirement
also covers the permission flags used by the scripted runner.

## Repeat a working and deliberate-failure example

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

Good should return `scripted_loop_passed` (exit 0). Wrong should return
`repair_failed` (exit 1). Both use six tool requests: read, search, refused host-file
read, edit, repeated edit, check. The repeated operation returns its earlier
receipt and does not edit again. Both should confirm four removed containers.
Replay protection is held within this run. It is not durable crash recovery;
that remains #106.

The budgets are 12 tool calls, one repair, one check and 600 seconds including
startup checks. There are zero model requests. Timeout/cancellation stops the
owned Pi process; the existing container path removes any active container and
confirms absence. A separate ten-second cleanup allowance remains available.
SIGKILL and crash recovery belong to #106.

In the report, open **Pi tools**, then a Receipt. Events show the refused read
and replayed operation. Candidate patch shows the one change. Context contains
the actual prompt, tool schemas, task and effective Node permissions. Pi
conversation shows each prepared request and runner reply. Evidence is local
and ignored under `reports/harness/pi/runs/`.

Takeaway: a model will choose actions, but our runner decides whether those
actions are allowed. A test failure is a valid result; a setup failure, malformed
reply or unfinished loop must never become a passing repair. This slice does
not finish #104 or prove live subscription access.

## Choose your subscription and model locally

Choose both the subscription provider and the exact model in `config.local.json`.
The file is ignored by Git; `config.example.json` is its template. It contains
preferences, never credentials. Codex is the current local choice, not a fixed
provider or model in the harness.

- Codex/ChatGPT subscription: set `provider` to `"openai"`.
- Claude subscription: set `provider` to `"anthropic"`.
- Set `model` to your chosen provider's exact model ID. To list IDs first,
  leave `model` as `null` and run `status` below.

Changing `provider` selects its own catalogue and sign-in. Changing `model`
selects a different model for that provider. The harness must preserve those
choices when live execution is added. Pi's supported sign-in methods are
documented in its [provider guide](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/providers.md).

```fish
node tools/harness/pi/setup.mjs status
node tools/harness/pi/setup.mjs login
```

The login command only signs in. Follow its browser instructions in your own
terminal. Pi needs a separate sign-in; we do not copy Codex's private token file.
Login reuses Pi's saved installation ID in `~/.pi/agent/settings.json`, creating
and saving one through Pi's settings manager if needed. OpenAI requires this
stable host ID. Existing Pi settings are preserved; malformed settings stop
login rather than resetting the identity. This is a Pi user setting and needs
no administrator or system configuration changes. The wrapper supplies the app
name "Mapbox TUI harness" and shows safe explanations for known login failures
without printing private token responses.
Pi's `openai` provider supports subscription OAuth, following OpenAI's
[Sign in with ChatGPT flow](https://developers.openai.com/siwc/token-sharing-open-source/sign-in).
Actual access depends on your account and grant. The static model list printed
by status is not an account-access test. No model request is made by setup.

After signing in, choose `model`, `maxModelRequests` and
`maxOutputTokensPerRequest` in `config.local.json`. Adam's current local defaults
are `gpt-6.1-sol`, ten model requests and 4,096 output tokens per request.
The configuration requires `auth: "subscription"`; API-key mode is rejected.
Request/output caps are preferences for the next live slice, not an implemented
total-token or spend guarantee. There is no live-run switch yet.

Credentials stay in Pi's private `~/.pi/agent/auth.json`, outside this repository,
receipts, prompts and containers. Do not paste them into chat. Keep the file
private. This checkpoint stops for review before a model-backed run.

## Check the implementation

```fish
python3 -m unittest discover -s tools/harness -p 'test_*.py'
node --test tools/harness/pi/loop.test.mjs
make test
make format-check
```

Go 1.27.1 is required. In the agent environment, prefix Go/Make commands with
`mise exec go@1.27.1 --`.

The Python checks cover file scope, symlink escapes, protected tests, stale
hashes, replay/conflicts, budgets, blocked startup and Pi-process cleanup. The
SDK checks exercise its real loop with a prepared provider, validate structured
outcomes and reject unsupported billing/configuration. The live examples above
are still needed to demonstrate the full SDK-to-container connection.
