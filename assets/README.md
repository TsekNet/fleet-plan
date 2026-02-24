# Assets

Logo, demo GIF, and tooling to regenerate the README demo.

## Demo GIF

The `demo.gif` shows `fleet-plan` running against `testdata` for Workstations and Servers. Regenerate with [VHS](https://github.com/charmbracelet/vhs).

### Prerequisites

- **VHS** – `brew install vhs` (macOS/Linux) or [winget install charmbracelet.vhs](https://github.com/charmbracelet/vhs#installation) (Windows)
- **ttyd** – [releases](https://github.com/tsl0922/ttyd/releases)
- **ffmpeg** – `brew install ffmpeg` or equivalent

On **WSL**, VHS needs Chromium shared libs. Install once:

```bash
sudo apt install -y libnss3
```

### Regenerate

From the repo root:

```bash
vhs assets/demo.tape
```

Output: `assets/demo.gif`

### Files

| File | Purpose |
|------|---------|
| `vhs-demo.go` | Demo output generator (`//go:build ignore`). Accepts `--team Workstations` or `--team Servers`. |
| `demo.tape` | VHS tape: types commands, captures output, renders GIF. |
| `demo.gif` | Generated GIF for README. |
| `logo.png` | Project logo. |

### Tape setup

The tape uses `Hide`/`Show` to run setup off-camera: it `cd`s to the repo, warms the Go build cache, and aliases `fleet-plan` to `go run ./assets/vhs-demo.go`. The visible recording shows `fleet-plan --repo testdata --team Workstations` then `--team Servers`.

If your repo path differs from `~/projects/code/github/fleet-plan`, edit the `cd` line in `demo.tape` before running.
