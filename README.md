# Recent Workspaces — a Herdr plugin

![herdr-plugin](https://img.shields.io/badge/herdr--plugin-✓-8b5cf6)

**Open Recent for [Herdr](https://herdr.dev).** A fuzzy-filterable list of the
folders you've opened as workspaces. Pick one and it opens — or re-focuses, if
it's already open — that workspace, right where you left off.

Herdr has no "recent folders" memory of its own (the socket API exposes no such
history), so this plugin keeps its own: it's seeded from the workspaces you have
open right now and grows every time you open a folder through the picker.

```
Recent Workspaces  v0.1.0
❯ api

❯ api            ~/Documents/Git/proj/api        ● open
  web            ~/Documents/Git/proj/web        2h ago
  infra          ~/Documents/Git/proj/infra      3d ago

↑↓ move · enter open · ctrl+x forget · esc quit
```

## Keys

**Recents list**

| Key | Action |
| --- | --- |
| `↑` / `↓` (`ctrl+p` / `ctrl+n`) | move the selection |
| type | fuzzy-filter by name or path |
| `enter` | open (or re-focus) the selected workspace |
| `ctrl+o` | browse the filesystem to open a folder not in the list |
| `ctrl+x` | forget the selected entry — and close its workspace if it's `● open` |
| `ctrl+u` | clear the filter |
| `esc` / `ctrl+c` | close the picker |

**Folder browser** (`ctrl+o`) — open something that isn't in the history yet;
opening it adds it to the recents. It starts at `~/Documents` by default (set
`HERDR_RW_BROWSE_ROOT` to start elsewhere) so browsing always begins from a
stable, shallow place instead of wherever the picker was launched.

| Key | Action |
| --- | --- |
| `↑` / `↓` | move the selection |
| type | fuzzy-filter the current folder's subfolders |
| `→` / `tab` | go into the highlighted folder |
| `←` (or `backspace` on an empty filter) | go up one folder |
| `enter` | open the highlighted subfolder as a workspace |
| `ctrl+o` | open the folder you're currently in as a workspace |
| `ctrl+a` | create a new folder here |
| `ctrl+r` | rename the highlighted folder |
| `ctrl+d` | delete the highlighted folder (and its contents) — asks to confirm |
| `esc` | back to the recents list |

## Install

```sh
herdr plugin install ismaelosuna7824/herdr-recent-workspaces
```

On install the `[[build]]` step **downloads a prebuilt binary** for your platform
(macOS/Linux, amd64/arm64) from the GitHub release — **no Go required**. If a
prebuilt binary isn't available it falls back to `go build` (needs Go 1.25+). The
binary is self-contained — no external processes.

This repo is tagged with the `herdr-plugin` topic, so it also shows up in Herdr's
plugin marketplace (`/plugins/`).

### Local development

```sh
git clone https://github.com/ismaelosuna7824/herdr-recent-workspaces
herdr plugin link herdr-recent-workspaces
herdr server reload-config
```

## Opening the picker

The plugin ships default keybindings (`prefix+o` for a split, `prefix+shift+o`
for a tab). If those don't fire in your setup, add an explicit binding to your
`~/.config/herdr/config.toml` — this is the form Herdr always honors:

```toml
[[keys.command]]              # open in a split beside your work
key = "prefix+o"
type = "shell"
command = "herdr plugin action invoke open --plugin ismaelosuna.recent-workspaces"

[[keys.command]]              # …or in its own tab
key = "prefix+shift+o"
type = "shell"
command = "herdr plugin action invoke open-tab --plugin ismaelosuna.recent-workspaces"
```

Then reload: `herdr server reload-config`. Pick keys that don't clash with your
other bindings.

You can also open it without a keybinding:

```sh
herdr plugin action invoke open --plugin ismaelosuna.recent-workspaces
```

## How it works

- **Recent list** lives in `recents.json` under the plugin's config dir
  (`HERDR_PLUGIN_CONFIG_DIR`). Newest first, capped at 100, dead folders pruned.
- **Currently-open workspaces** are read from Herdr's `session.json` (the only
  place a workspace's directory is available — the socket API omits it). They're
  folded into the list and flagged `● open`, and persisted to `recents.json` each
  time the picker opens so they survive being closed later.
- **Opening** shells out to the Herdr CLI: `workspace focus <id>` when the folder
  is already an open workspace, otherwise `workspace create --cwd <path> --focus`.

> **Known limitation.** An open workspace is only captured into the history the
> next time you open the picker — the plugin can't observe Herdr's native
> **Close** (the socket API fires no such event). So if you open a folder outside
> the picker and close it via Herdr's menu without opening the picker in between,
> that folder never reaches the recents. Open the picker once while it's open and
> it's remembered for good.

## Development

```sh
go test ./...
go build -o bin/recent-workspaces ./cmd/recent-workspaces
```

## License

MIT — see [LICENSE](LICENSE).
