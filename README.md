# please_eject

A tiny macOS tool that frees a **stuck external drive** so it can eject —
without the "one or more programs may be using it" nag and the scary
force-eject.

It finds every program holding the volume open, lets you stop them (all at once
or one at a time), then rechecks and offers to eject when the drive is clear.

## Install

Requires macOS and Go.

```sh
go build -o please_eject .
# optional: put it on your PATH
mv please_eject /usr/local/bin/
```

## Usage

Pass the **volume name** (as it appears in Finder / under `/Volumes`):

```sh
please_eject "My Passport for Mac"
```

You'll get an interactive list of the programs using the drive.

### Keys

| Key            | Action                                              |
| -------------- | --------------------------------------------------- |
| `↑` / `↓`      | Move up/down the list (also `j` / `k`)              |
| `space`        | Mark / unmark a program for stopping                |
| `x` / `enter`  | Stop the marked programs (or the highlighted one)   |
| `a`            | Stop **all** your programs at once (asks to confirm)|
| `s`            | Tell Spotlight to stop indexing the drive (sudo)    |
| `r`            | Rescan                                              |
| `e` / `enter`  | Eject (shown once the drive is clear)               |
| `q` / `esc`    | Quit                                                |

### How it stops programs

It asks each program to close nicely first (SIGTERM), waits a few seconds, and
only force-quits (SIGKILL) if something is stuck. After every action it rescans,
so the list always reflects reality.

**System processes** (Spotlight, Quick Look, Time Machine, Finder, etc.) are
marked with a warning and left out of "stop all" — stop those deliberately with
`x` if they're the last holdouts.

## Common holdouts

- **A media player / app that lives on the drive** — it can't let go until it
  fully quits; `please_eject` handles the force-quit for you.
- **Quick Look** — appears if you previewed a file with spacebar in Finder.
  Harmless to stop; it respawns.
- **Spotlight** (`mds` / `mdworker`) — if indexing is the blocker, press `s`.
  It runs `sudo mdutil -i off` on the drive (you'll enter your password), which
  tells Spotlight to release it. Re-enable later with:

  ```sh
  sudo mdutil -i on "/Volumes/My Passport for Mac"
  ```

  If the drive isn't Spotlight-indexed, `s` just says so instead of prompting.

## Rebuilding after changes

Whenever you edit the source, rebuild the binary:

```sh
go build -o please_eject .        # rebuild
```

If you installed it on your PATH, copy the fresh build over:

```sh
go build -o please_eject . && mv please_eject /usr/local/bin/
```

Before committing changes, keep it clean and green:

```sh
go test ./...        # run the tests
go vet ./...         # static checks
gofmt -l .           # lists unformatted files (empty = good); `gofmt -w .` fixes
```

## Notes

- macOS only (uses `diskutil`, `lsof`, `ps`, `mdutil`).
- If the volume name isn't found, the tool lists your mounted volumes so you can
  copy the exact name.
