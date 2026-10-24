# bfiles

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/platform-Linux-lightgrey?logo=linux&logoColor=white)](https://github.com/abunjevac/bfiles)

A fast two-panel terminal file manager in the spirit of Midnight Commander, with a modern look, neovim-style keys
and a which-key popup. Powered by Go and Bubble Tea.

---

## Features

- **Two panels, always** — each shows a local directory, an SSH remote, the inside of an archive, or search results
- **Instant startup** — the first frame is drawn before any slow work; a 100k-entry directory stays smooth
- **Never blocks** — listings, previews, directory sizes, SSH, archives and file operations all run in the background
- **Queued file operations** — copy, move, delete, pack and extract run in a floating queue with progress, speed and
  ETA; pause, cancel and reorder jobs, and change how many run in parallel without touching the config
- **Conflict prompts** — overwrite, skip, rename, overwrite-if-newer, optionally for the rest of the job
- **SSH/SFTP** — connect from `~/.ssh/config` hosts or by typing `user@host`; history without duplicates; ssh-agent,
  keys, passphrases and passwords; `known_hosts` verification
- **Archives as folders** — zip, tar, tar.gz/bz2/xz/zst can be read and written (atomic rebuild); 7z and rar are
  read-only; edit files inside archives
- **Preview** — quick view in the other panel or a full-screen viewer: syntax highlighting, images as colour
  half-blocks, archive and directory listings, hex dumps, and external previewers such as `glow` or `pdftotext`
- **Search** — by name (glob, substring, regex) and content; results appear as a panel you can operate on; the
  dialog remembers its last settings
- **Directory sizes** — for the cursor, the selection, or every directory in the panel
- **Hidden files and special dirs** — separate toggles for dotfiles and for `.git`, `node_modules` and friends
- **Themes** — 11 builtin themes, applied live from a picker that previews as you move
- **Settings** — an in-app editor that keeps the comments in `config.toml`, or your own editor with live reload
- **Terminal and file explorer** — open either in the current directory, also for SSH panels
- **Nerd Font icons** — with a plain unicode fallback

---

## Requirements

- Linux
- Go 1.27+ (to build)
- A terminal with true colour; a [Nerd Font](https://www.nerdfonts.com/) for icons (`icons = "unicode"` otherwise)
- Optional: `glow`, `pdftotext`, `ffmpegthumbnailer` for richer previews; `xdg-open` for opening files

---

## Installation

```bash
git clone https://github.com/abunjevac/bfiles.git
cd bfiles
task install        # builds and installs to ~/.local/bin — requires Task (https://taskfile.dev)
# or: go build -o bfiles ./cmd/bfiles
```

---

## Usage

```bash
bfiles                                  # both panels in the current directory
bfiles ~/src                            # both panels in ~/src
bfiles ~/src ~/backup                   # left and right
bfiles . nas:/srv/media                 # local left, SSH host alias right
bfiles ssh://pi@10.0.0.5:2222/home/pi   # SSH URL
bfiles ~/Downloads/release.tar.gz       # open an archive as a folder
bfiles --config ~/work-bfiles           # use another config directory
bfiles --theme nord --hidden            # per-run theme and hidden files
```

| Command                     | Purpose                                         |
|-----------------------------|-------------------------------------------------|
| `bfiles keymap`             | Print the default keymap                        |
| `bfiles keymap --write`     | Reset `keymap.toml` to the override template    |
| `bfiles themes`             | List builtin and custom themes                  |
| `bfiles config`             | Print (and create) the config directory         |

---

## Keys

Press `Space` and wait a moment: the which-key popup lists everything that can follow. `?` (or `F1`) opens a
searchable list of all bindings; choosing an entry runs it. Every binding can be changed in `keymap.toml`: it holds
only your overrides, so actions you don't list keep their defaults (and pick up new ones in future versions). A key
you assign is taken away from the action that had it by default.

### Navigation

| Keys                       | Action                                   |
|----------------------------|------------------------------------------|
| `j` `k` / `↓` `↑`          | Move                                     |
| `l` / `→` / `Enter`        | Enter directory, open archive, open file |
| `h` / `←` / `Backspace`    | Parent directory (leaves archives)       |
| `g g` / `G`                | First / last entry                       |
| `Tab`                      | Switch panel                             |
| `H` / `L`                  | History back / forward                   |
| `~`                        | Home directory                           |
| `Ctrl+G`                   | Go to a path or `ssh://` target          |
| `'`                        | Bookmarks                                |
| `=`                        | Show this location in the other panel    |
| `/`                        | Quick filter                             |
| `Ctrl+R`                   | Reload                                   |

### Selection

| Keys                      | Action                         |
|---------------------------|--------------------------------|
| `v` / `Insert`            | Toggle and move down           |
| `Shift+↑` `Shift+↓`       | Extend selection               |
| `*`                       | Invert                         |
| `+` / `-`                 | Select / unselect by pattern   |
| `Ctrl+A`                  | Select all                     |
| `Esc`                     | Clear filter, then selection   |

### Files

| Keys                      | Action                                 |
|---------------------------|----------------------------------------|
| `F5` / `Space c`          | Copy                                   |
| `F6` / `Space m`          | Move                                   |
| `Shift+F6` / `Space r`    | Rename                                 |
| `F7` / `Space n d`        | New directory                          |
| `Shift+F4` / `Space n f`  | New file                               |
| `F8` / `Delete` / `Space d` | Delete                               |
| `F4` / `e`                | Edit                                   |
| `o`                       | Open with the default application      |
| `F3`                      | Full-screen viewer                     |
| `p`                       | Quick view in the other panel          |
| `Space s` / `Space S`     | Directory size / all directory sizes   |
| `Space f` / `Alt+F7`      | Search                                 |
| `Space a p` / `Space a x` | Pack / extract                         |
| `Space p`                 | Permissions: checkbox grid incl. setuid/setgid/sticky, or type octal digits; optionally recursive |
| `y`                       | Copy path to the clipboard             |
| `i` / `Space i`           | Info: type, exact size, contents, mode, owner, times, inode |

### View, remote and app

| Keys                      | Action                                    |
|---------------------------|-------------------------------------------|
| `.` / `,`                 | Toggle hidden files / special dirs        |
| `Space v …`               | Sorting, mtime column and other view keys |
| `Space v b`               | Exact byte sizes instead of K/M/G (per panel, not saved) |
| `Ctrl+K`                  | SSH connect                               |
| `Ctrl+W`                  | Close the SSH connection, archive or search (back to the local dir) |
| `Ctrl+T` / `Ctrl+E`       | Terminal / file explorer here             |
| `F2` / `Space q`          | Job queue                                 |
| `Space , t`               | Theme picker                              |
| `F9` / `Space , c`        | Settings                                  |
| `Space , e`               | Edit `config.toml` in your editor         |
| `Ctrl+Q` / `F10`          | Quit (asks only when jobs are pending)    |

### Queue window

With the queue focused (`F2`): `j`/`k` select, `p` pause/resume, `x` cancel (or dismiss a finished job),
`K`/`J` reorder, `+`/`-` change the parallel limit, `c` clear finished jobs, `Esc` close the window. It opens
again by itself when a job is queued.
Changing the limit never interrupts running jobs; it only decides how many waiting jobs may start.

---

## Configuration

bfiles creates its configuration in `~/.config/bfiles/` on first launch; `--config DIR` uses another directory.

| File           | Purpose                                     |
|----------------|---------------------------------------------|
| `config.toml`  | Settings (commented; see below)             |
| `keymap.toml`  | Your key binding overrides (defaults listed, commented out) |
| `themes/`      | Builtin themes, plus any you add            |
| `history.toml` | SSH connection history                      |

Changes to any of these files are applied while bfiles runs. A broken file shows an error and the previous
settings stay in effect. The settings form (`F9`) edits `config.toml` in place and keeps its comments.

```toml
theme = "tokyonight"
icons = "nerd"                               # nerd, unicode or none
editor = []                                  # empty: $VISUAL, $EDITOR, vi
opener = ["xdg-open", "{file}"]
terminal = ["bterm", "-w", "{cwd}"]
terminal_exec = ["bterm", "-w", "{cwd}", "-e", "{cmd}"]  # used for SSH panels
explorer = ["dolphin", "{url}"]              # {url} may be sftp://user@host:port/path
confirm_delete = true
use_trash = false
parallel_jobs = 1
which_key_delay_ms = 250
dir_size = "apparent"                        # or "disk"

[panels]
show_hidden = false
show_special = false
show_mtime = true
sort = "name"
special_dirs = [".git", "node_modules", ".idea", ".vscode", "__pycache__", ".venv", ".cache", ".task", ".superpowers", "target", "dist", "vendor"]

[bookmarks]
work = "~/workspace"
nas = "ssh://pi@nas/srv/media"

[preview]
max_bytes = "1M"                             # preview size limit; units K, M, G

[[preview.rule]]
match = ["*.md", "*.markdown"]
command = ["glow", "-s", "dark", "-w", "{width}", "{file}"]

[ssh]
history_limit = 50
keepalive_sec = 30
```

### Themes

A theme is a TOML file of semantic colours (`bg`, `fg`, `accent`, `dir`, `archive`, `header_active_bg`, …). Values are
`#rrggbb`, an ANSI index `0`–`255`, or `default` for the terminal's own colour. Copy a builtin from `themes/`, change it,
and pick it in the theme picker. The `terminal` theme follows your terminal's palette.

### SSH

Hosts come from `~/.ssh/config` (`HostName`, `User`, `Port`, `IdentityFile` and a single-hop `ProxyJump`) and from
the connection history. Authentication tries ssh-agent, then your keys (asking for passphrases), then
keyboard-interactive and password prompts. Passwords are never stored. Unknown host keys are shown with their
fingerprint and added to `~/.ssh/known_hosts` once accepted; a changed host key is refused. A connection stays open
while a panel shows it or a job uses it; `Ctrl+W` takes the panel back to the local directory it showed before.

The terminal key (`Ctrl+T`) on an SSH panel opens `terminal_exec` running `ssh` into the same directory. If ssh fails,
the terminal stays open and shows the exit status.

### Archives

zip, tar, and tar compressed with gz, bz2, xz or zst can be read and written. Changes are collected while a job runs
and written once at the end: the new archive goes to a temporary file next to the original and replaces it only when
complete. 7z and rar are read-only. Archives inside archives and archives on SSH remotes work the same way.

---

## Development

```bash
task build    # build binary
task run      # build and run (task run -- ARGS)
task test     # run tests
task race     # run tests with the race detector
task lint     # golangci-lint + go vet
task check    # lint + test + osv-scanner
task tidy     # go mod tidy
```

---

## License

[MIT](LICENSE) © Alan Bunjevac
