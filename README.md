# bifrost-quota-monitor

A tmux status bar segment showing how much of your [Bifrost](https://github.com/maximhq/bifrost)
virtual key budget you have spent.

```
Bifrost: $76.97/$250.00
```

Green below 70% of the budget, yellow from 70%, red from 90%.

Bifrost is an LLM gateway. Its governance feature gives a virtual key a spending budget that
resets on a schedule. Nothing surfaces that number while you work, so you find out you are
near the limit when a request starts failing. This puts it in front of you instead.

## How it works

A background daemon polls the gateway every five minutes and writes a small JSON cache. The
tmux status bar runs `bifrost-quota-monitor status`, which reads only that cache and prints
one line. The status command never touches the network, so a slow or unreachable gateway
cannot make your status bar hang.

The budget figure comes from the gateway itself, at
`GET /api/governance/virtual-keys/quota`. That endpoint is self-service: the virtual key in
the request authorises reading its own quota, so no admin token is involved.

A failed poll does not blank the segment. The previous reading keeps showing until it passes
a 15 minute staleness threshold, so one bad poll out of the roughly 300 a day is invisible
rather than a flicker. Retries back off from 30 seconds to a 5 minute ceiling with jitter, and
a `429` waits out the interval in the response's `Retry-After` header when it carries one.

## Requirements

- macOS. The background service is a launchd user agent, and there is no systemd equivalent
  in this version. Everything except `init`'s service step is platform independent, so a Linux
  port is mostly a matter of adding a unit file.
- tmux.
- A Bifrost deployment with governance enabled, and a virtual key that has a budget.
- Go 1.26 or newer to build.

## Install

```sh
go build -o bifrost-quota-monitor .
mkdir -p ~/.local/bin && install -m 755 bifrost-quota-monitor ~/.local/bin/
```

## Setup

Point the tool at your gateway and give it a key:

```sh
export BIFROST_BASE_URL=https://bifrost.example.com
export BIFROST_API_KEY=sk-bf-...
bifrost-quota-monitor init
```

There is deliberately no default gateway address. Bifrost is self hosted, so there is no
sensible address to guess, and an unset one fails with a message naming both places it can be
set rather than an opaque dial error.

`init` does four things, and skips any that are already done:

1. Reads the key and resolves the gateway.
2. Fetches the quota once, so a bad key or URL fails now rather than silently in the daemon.
3. Writes the config, stores the key for the agent, and patches your tmux config.
4. Installs and starts the launchd agent.

Re-running is safe. Add `--force` to redo every step.

## Commands

| Command | Description |
|---|---|
| `bifrost-quota-monitor init` | One-time setup |
| `bifrost-quota-monitor status` | Print the status segment, reading the cache |
| `bifrost-quota-monitor status --no-color` | Same, without tmux style escapes |
| `bifrost-quota-monitor refresh` | Signal the daemon to poll immediately |
| `bifrost-quota-monitor daemon` | Run the poller, normally started by launchd |

`init` binds `<prefix> F6` to `refresh`.

`status` takes `--no-color` (also spelled `--no-colour`, or `--plain`) to print the segment
without tmux style escapes, for a shell prompt, a pipe, or a script where `#[fg=green]` would
show up literally:

```sh
$ bifrost-quota-monitor status --no-color
Bifrost: $83.43/$250.00
```

## Configuration

`~/.config/bifrost-quota-monitor/config.json`:

```json
{
  "poll_interval_seconds": 300,
  "cache_path": "~/.cache/bifrost-quota-monitor/status.json",
  "base_url": "https://bifrost.example.com",
  "api_key_env": "BIFROST_API_KEY",
  "api_key_file": "~/.config/bifrost-quota-monitor/key"
}
```

An absent config file means defaults, so the binary works before `init` runs.
`BIFROST_BASE_URL` overrides `base_url` when set.

### Where the key comes from

The config records where to find the key, never the key itself, so it can be committed or
shared without redaction.

The environment is checked first, using the variable named by `api_key_env`. When that is
unset, the key is read from `api_key_file`.

The file exists because launchd starts jobs with only the user-level environment, never a
login shell's, so an exported variable is invisible to the agent. Without the file the agent
reports the key as unset on every poll and the segment degrades to `??` once the last reading
ages out. `init` writes the file at mode 0600, and the tool refuses to read one that group or
others can read rather than quietly honouring it.

## What the segment shows

| Segment | Meaning |
|---|---|
| `Bifrost: $76.97/$250.00` | Spent against the budget |
| `Bifrost: $76.97` | The budget is unlimited, so there is no denominator |
| `Bifrost: --` | No cache file, so the daemon has not run yet |
| `Bifrost: ??` | The reading is stale, or a fetch failed with nothing to fall back on |

Both placeholders are grey, so missing data does not read as a quota state.

The denominator is the budget's effective limit, which is `max_limit` plus `override_amount`
when an override is active. The endpoint reports only the base limit while the gateway
enforces the larger one, so using `max_limit` alone would show a red segment while requests
were still being served.

### What it does not show

There is no reset countdown. Bifrost budgets reset either on a calendar boundary or on a
rolling window, and the flag that decides which is not serialised in the quota response, so a
client cannot tell which applies. Showing a reset time that is wrong some of the time next to
a dollar figure that is always right would undermine both, so `reset_duration` and
`last_reset` are cached but not rendered.

## tmux integration

`init` appends a marked block to `~/.config/tmux/tmux.conf`, or `~/.tmux.conf` if that is the
one you use:

```tmux
# bifrost-quota-monitor begin
set -g status-right-length 200
set -ga status-right " #(/Users/you/.local/bin/bifrost-quota-monitor status)"
bind-key F6 run-shell "/Users/you/.local/bin/bifrost-quota-monitor refresh"
# bifrost-quota-monitor end
```

Two details matter if you maintain your tmux config by hand.

It only ever appends to `status-right` with `-ga`, and never assigns it. That is what lets it
coexist with your own value and with other tools that add their own segment. Repeated appends
do not stack, because any plain `set -g status-right` earlier in your file resets the option
each time tmux sources it.

The command is written as an absolute path rather than a bare name, because tmux forks a shell
without your interactive `PATH`. A bare name that is not found produces an empty segment with
nothing to tell you why.

The original file is backed up to `<path>.bifrost-quota-monitor.bak` before any change, and
the block is delimited by markers so re-running replaces it instead of adding a second copy.

## Managing the service

```sh
launchctl print gui/$(id -u)/com.github.tedwardd.bifrost-quota-monitor
launchctl kickstart -k gui/$(id -u)/com.github.tedwardd.bifrost-quota-monitor
tail -f ~/Library/Logs/bifrost-quota-monitor.log
```

## Troubleshooting

Read the cache directly. It records what happened on the last poll:

```sh
jq . ~/.cache/bifrost-quota-monitor/status.json
```

`error` is a hard failure with nothing to display. `last_error` is a failed poll that left an
earlier reading in place, which is the normal transient case and not something to act on
unless it persists.

| Symptom | Cause |
|---|---|
| `Bifrost: --` | The daemon has not run. Check the agent is loaded. |
| `Bifrost: ??` right after setup | `error` in the cache names the reason: no key, a rejected key, or no budget on the key. |
| `??` after working for a while | Polls stopped. Usually the agent died, or a laptop slept longer than the staleness window. `refresh` recovers it. |
| `error` mentions the key is unset | The agent cannot see your shell's environment. Re-run `init` so it writes `api_key_file`. |
| Segment is empty rather than showing a placeholder | tmux cannot find the binary. Check the path in the managed block exists. |
| Every fetch times out while `curl` works | A local firewall is blocking this binary. On macOS, Little Snitch prompts per binary and a dismissed prompt becomes a deny. |

That last one is worth a note, because it looks exactly like a broken client. Two checks tell
them apart: `curl` the endpoint with `-H "x-bf-vk: $BIFROST_API_KEY"` and see whether it
returns 200, and run `go test ./internal/api/` which reaches the network from a different
binary. If those succeed while the installed binary times out, it is the firewall rather than
the code.

## Network access

The daemon makes exactly one kind of request: `GET {base_url}/api/governance/virtual-keys/quota`,
authenticated with the `x-bf-vk` header. There is no telemetry and no update check. The
`status` command makes no requests at all.

## Development

```sh
go test ./...
```

60 tests, no dependencies beyond the standard library. The tests use `httptest` for the HTTP
layer and `t.TempDir()` with `t.Setenv("HOME", ...)` for filesystem isolation, so they do not
touch your real config, cache, or key.

## License

MIT. See [LICENSE](LICENSE).
