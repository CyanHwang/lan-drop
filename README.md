# LanDrop

> Share a folder with every device on your network. No client, no account, no cloud.

**English** · [简体中文](README.zh-CN.md)

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Wails](https://img.shields.io/badge/Wails-v3-DF0000)](https://v3.wails.io)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux-lightgrey)](#quick-start)

Point LanDrop at a folder and start the service. The other end opens a browser and gets the whole tree: browse, upload, download, and pick up an interrupted transfer where it stopped.

<p align="center">
  <img src="docs/mobile.png" width="300" alt="LanDrop on a phone, light theme">
  <img src="docs/mobile-dark.png" width="300" alt="LanDrop on a phone, dark theme">
</p>
<p align="center"><sub>The browser UI with multi-select. Checkboxes pick files and folders for batch download; tapping a folder name opens it. Theme follows the system; language switches from the top bar.</sub></p>

**Written entirely by AI.** Every line of Go, TypeScript, CSS, build configuration and documentation in this repository was produced by an AI coding agent.

---

## Features

| | |
|---|---|
| **Nothing to install** | The other device only needs a browser. Scan the QR code or type the URL |
| **Multiple folders** | Every shared folder appears under one virtual root, browsable like a normal file tree |
| **Streaming transfers** | Constant 256 KB of memory. A 100 GB file costs the same as a 1 MB one |
| **Batch download** | Select files and folders with checkboxes and download them as one zip, streamed uncompressed for LAN speed |
| **Resumable** | Uploads run in 64 MB chunks, staged in `.part` and renamed only after the length checks out; downloads use standard HTTP `Range` |
| **Live progress** | The desktop window shows direction, speed, elapsed time and the peer's IP for every transfer |
| **Read-only mode** | Serve files without letting the network write to them |
| **Two languages** | English and Simplified Chinese, detected on first launch and switchable at runtime |
| **Portable** | Settings live in `lan-drop.ini` next to the executable. Copy the folder, keep your setup |
| **Tray-resident** | Closing the window keeps the service running; quit from the tray menu to stop it |
| **Headless mode** | The same binary runs without a GUI on a NAS, a Raspberry Pi or in Docker |

## Why it is small

LanDrop is a [Wails](https://v3.wails.io) v3 application: a Go backend and a
React frontend compiled into a single binary.

What matters is what it leaves out. Wails does not bundle a browser engine — it
drives the one already on the system: WebView2 on Windows, WebKitGTK on Linux.
No Chromium, no Node runtime, no `node_modules` at runtime.

The result is an executable of about 13 MB that starts instantly and idles at a
few tens of megabytes. An Electron app of comparable scope typically ships
100 MB or more before it touches a single one of your files.

## Quick start

### Download

Grab the file for your platform from [Releases](https://github.com/CyanHwang/lan-drop/releases):

- **Windows** — a single executable, e.g. `LanDrop-windows-amd64.exe`. Download and run; nothing to unzip.
- **Linux** — a `.deb` package, e.g. `LanDrop-linux-amd64.deb`. Install it with `sudo apt install ./LanDrop-linux-amd64.deb`; the desktop entry, icon and GTK/WebKit dependencies come with it.

No runtime to set up first.

On first launch the OS may ask for firewall or network permission. **You must allow it**, otherwise no other device can connect.

### Use it

1. Click **Add folder** and pick a directory (you can add several)
2. Click **Start service**
3. Connect your phone to the same Wi-Fi and scan the QR code

That is the whole flow. No accounts, no pairing, no third step.

### Build from source

Requires Go 1.26+, Node.js 18+ and the [Wails v3 CLI](https://v3.wails.io/getting-started/installation/).

```bash
git clone https://github.com/CyanHwang/lan-drop.git
cd lan-drop

# Development, with hot reload
wails3 dev

# Production build, output in bin/
wails3 build
```

The icon source is `build/appicon.svg`; run `wails3 generate icons` after editing it.

### Headless server

No GUI, just the HTTP service. Useful for a NAS, a Raspberry Pi or a container:

```bash
go build -tags server -o LanDrop-server .
./LanDrop-server -share /data/photos -share /data/docs
```

```
LanDrop is running (read-write)

Shared folders (2):
  photos             /data/photos
  docs               /data/docs

Reachable at:
  http://127.0.0.1:57577
  http://192.168.1.10:57577

Press Ctrl+C to stop.
```

| Flag | Default | Description |
|---|---|---|
| `-addr` | `:57577` | Listen address: `:57577`, `0.0.0.0:9000`, `127.0.0.1:57577` |
| `-share` | none | Folder to share. Repeat the flag or separate with commas. Overrides the config file |
| `-config` | `lan-drop.ini` next to the binary | Config file path |
| `-readonly` | `false` | Reject uploads, mkdir and delete |
| `-auto-port` | `false` | Scan forward when the port is taken. Off by default so containers fail loudly instead of drifting |

With Docker:

```bash
docker build -f build/docker/Dockerfile.server -t lan-drop .
docker run --rm -p 57577:57577 -v /host/photos:/data/photos lan-drop -share /data/photos
```

## Configuration

Settings live in `lan-drop.ini` **next to the executable**, so the whole thing is portable. The desktop app writes this file when you add or remove a folder; editing it by hand works too.

```ini
; LanDrop configuration file (portable)

[general]
language = en            ; en | zh-Hans
show_hidden = false      ; include entries with the OS hidden attribute
show_dotfiles = false    ; include entries whose name starts with a dot
filter = *.tmp, node_modules   ; hide these from the listing

[share_1]
name = Downloads
path = D:\Downloads

[share_2]
name = Work
path = D:\Work\Docs
```

Both listing filters apply to folders as well as files, and both default to
`false`. `show_hidden` only has an effect on Windows, where files carry a hidden
attribute; on Unix a leading dot is the only convention, so `show_dotfiles`
covers it.

`filter` hides entries by name. Separate rules with commas or semicolons; each
rule is a literal name or a glob (`*`, `?`). Prefix a rule with `re:` to use a
regular expression, and put a single expression with no separators in it to
match against the whole input. Anything that does not compile is skipped, so a
typo never breaks the listing:

```ini
filter = *.tmp; node_modules; re:^\.(git|svn)$
```

See [`lan-drop.ini.example`](lan-drop.ini.example). The real `lan-drop.ini` is git-ignored — it holds machine-specific paths.

Older builds used `config.ini` in the same directory. On first launch the app
reads it once and writes `lan-drop.ini`; the old file is left untouched.

### Language

The OS language is detected on first launch and written to `lan-drop.ini`. From then on the stored value wins — changing your system locale will not flip the UI behind your back.

Two ways to switch:

- **Desktop window** — the language toggle in the top bar writes the choice back to `lan-drop.ini`
- **Browser UI** — a toggle in the top bar, remembered per browser in the `landrop_lang` cookie

The browser starts from the host's configured language. A manual choice in that browser wins, and it never writes to the host's config.
On a `-tags server` build whose `lan-drop.ini` names no language, the browser falls back to `Accept-Language`, so a Chinese phone opens in Chinese.

### Viewing files

Each row keeps a single "⋯" trigger: files offer view, download and delete, folders only delete. A read-only server omits delete. Clicking the name of a viewable file opens the preview too.

Files with a preview open in a full-screen viewer:

- **Images** — drag to pan, wheel to zoom around the cursor, percentage in the toolbar; the footer shows the name, creation time, modification time and size. Double-click toggles between fit and 100%. Formats the browser cannot show (HEIC, AVIF) are converted by ffmpeg; this is an explicit click, so it works even with automatic thumbnails switched off.
- **Markdown** — rendered with a local `marked` and sanitised with `DOMPurify`, so HTML inside a document cannot run script in the page.
- **docx** — local `docx-preview` keeps Word paragraph and table layout; **xlsx / xls** — local `SheetJS` renders the first sheet at full width, up to 500 rows.
- **PDF** — the browser's built-in viewer.
- **Video** — the entry exists, but playback is still undecided; the viewer says so for now.

All preview libraries are vendored under `internal/transfer/static/vendor/`, loaded only when needed, and work offline. Licences are listed in that directory.

### Thumbnail previews

Off by default. Turn them on in the desktop window's options area and the browser listing shows square previews for images and videos. The switch is persisted as `thumbnails` in `lan-drop.ini`, works whether the service is running or not, and clears the cache when turned off.

- JPEG / PNG / GIF / WebP / BMP / TIFF are decoded in-process; no extra component
- With `ffmpeg` on PATH, video, HEIC/HEIF and AVIF previews are added; without it those rows keep the generic icon
- Previews live in the OS cache directory (`%LOCALAPPDATA%\lan-drop\thumbs` / `~/.cache/lan-drop/thumbs`). They are evicted after 7 days or when the directory passes 256 MB. Clearing the system cache removes them too, without touching any original file
- The cache key is path + size + modification time, so overwriting a file with the same name just generates a new preview. The one case it cannot see is a replacement with both the same size and the same timestamp (timestamp-preserving copies, FAT's 2-second granularity); the old icon then lasts until the file changes again
- Each preview is ~4 KB and decoding is limited to two workers, so transfers keep their CPU

On a `-tags server` build, set the ini key or pass `-thumbs` to force it on.

## HTTP API

The browser page and every endpoint share one port. No extra services.

| Method | Path | Description |
|---|---|---|
| GET | `/` | Share picker (a single share redirects straight to it) |
| GET | `/s/{share}/{dir}` | Rendered directory listing |
| GET | `/static/` | Stylesheet and script, immutable-cached per build |
| GET | `/api/thumb?share=&path=&v=` | Square preview; `v` is size and mtime, used only as a browser cache key |
| GET | `/api/list?share=&dir=` | Directory listing. Empty `share` returns the virtual root |
| GET | `/api/download?share=&path=&inline=1` | Single-file download, supports `Range`; `inline=1` serves it for the viewer instead of downloading |
| GET / POST | `/api/download/zip?share=&dir=&paths=...` | Batch zip. `paths` repeats; POST accepts a form body for large selections |
| GET | `/api/probe?share=&dir=&name=&total=` | Staged bytes for a file; with `total` it also reports `complete` |
| PUT | `/api/upload?share=&dir=&name=` | Chunked upload. `X-Offset` sets the resume point, `X-Total-Size` declares the file size, `X-Chunk-CRC32` carries the chunk checksum |
| POST | `/api/upload/cancel?id=` | Marks an upload as cancelled on purpose, deletes its staged data, and tells the desktop to show it as cancelled rather than disconnected |
| POST | `/api/mkdir?share=&dir=&name=` | Create a folder |
| DELETE | `/api/file?share=&path=` | Delete a file or an empty folder |
| GET | `/api/info` | Status, share count, read-only flag, language |

**Upload contract**: bytes land in `<name>.part` first and are `fsync`ed and renamed onto the real name only when the written total matches `X-Total-Size`. A dropped transfer therefore never leaves a half-written file under the real name, and never shows one in the listing. `X-Offset` must equal the staged byte count, otherwise the server returns `409` with the real size.

**Cross-file guard**: the client sends `X-File-Size` and `X-File-Mtime` as a fingerprint. When the staged bytes belong to a different file that happened to share the name, the server answers `409` with `reset: true` and drops them instead of splicing two files together.

**Content check**: the browser computes a CRC32 over each chunk and sends it as `X-Chunk-CRC32`; the server hashes while writing, and on a mismatch rolls the staging file back to the chunk boundary and answers `422` so the client resends that chunk. TCP's 16-bit checksum is usually computed by the network card and misses more than people assume.

**Keep or discard**: an unexpected failure — a dropped link, a client that vanishes — keeps the `.part` file so picking the same file again resumes. Cancelling, and stopping the service, are deliberate decisions: the staged data is deleted with them instead of sitting invisibly on the disk.

**Concurrency and space**: one writer per destination path — a second device gets `409` with `busy: true`. Free space is checked before the first byte, so a file that cannot fit fails with `507` immediately rather than at 99%.

**Chunks**: the browser sends 64 MB chunks, so a failure costs at most one chunk. Forty-five seconds without progress means the socket is dead; it reconnects and resumes, and never gives up while bytes keep moving.

**Batch downloads** are stored uncompressed (`zip.Store`) — on a LAN the link is
the bottleneck, and deflating video or archives just burns CPU. Because the zip
is streamed, it has no `Content-Length`: the browser cannot show a percentage
and a dropped transfer restarts from zero. Single-file downloads keep their
`Range` support, so grab one large file directly rather than through the zip.

The browser client decides where to resume by asking how many bytes are staged for that *file name*. A fingerprint (size plus modification time) rules out the common "same name, different file" case; two different files with an identical size and mtime are still indistinguishable, and telling them apart would mean hashing gigabytes on both ends.

There is no API version prefix and no CORS headers — the endpoints exist for the embedded page, not for third parties.

## Architecture

![Architecture](docs/architecture.svg)

One process, two frontends:

- **Desktop** (React in a Wails WebView) picks folders, starts and stops the server, renders the QR code and shows live transfer progress.
- **Browser** (a single-page HTML file embedded in the Go binary) handles browsing, uploading and downloading on the phone. It is served by the LAN listener, not by the WebView asset handler.

Both share one `transfer.Server`, which is why the desktop window can show progress for uploads coming from a phone — pushed back as the `transfer:progress` Wails event.

```
main.go              Wails entry point: tray, single instance, window lifecycle
appservice.go        Service exposed to the frontend: folders, start/stop, clipboard
main_server.go       -tags server entry point: plain HTTP, no GUI
internal/config      lan-drop.ini read/write, legacy migrations
internal/i18n        Message catalogue and OS language detection
internal/transfer    HTTP server, path validation, streaming, progress
  server.go          Listener lifecycle, routing, path resolution
  handlers.go        The single-file and write endpoints
  zip.go             Streaming batch zip, folder walking, entry naming
  netutil.go         Port probing, LAN address ranking
  view.go            Page rendering: template data, paths, language
  templates/         html/template sources (shell, listing, icons)
  static/            Browser CSS and JS, cached by build id
```

## Security

**There is no authentication.** Any device on the same subnet that knows the address can read and write the shared folders. That is what makes it convenient, and it is also the only real risk.

- Use it on networks you trust — home, or your own hotspot. Not on office or public Wi-Fi
- Sharing with a device you do not trust? Start with `-readonly`; write endpoints return `403`
- Paths are validated twice: lexically against `../`, then through symlink resolution that also covers **not-yet-existing** upload targets
- Absolute host paths are never sent to clients — the browser only sees folder names
- The activity list shows each peer's IP and a short device hint derived from `User-Agent`. Both come from the connection itself, are never written to disk, and disappear when the app exits
- Delete only removes files and empty directories. `os.Remove` is not recursive, and the share root itself is refused

Found a security issue? Open an [issue](https://github.com/CyanHwang/lan-drop/issues) without public exploit details.

## Development

```bash
go test ./...              # unit tests
go test ./... -short       # skip the 256 MB throughput test
go vet ./...
go build -tags server .    # verify the headless build
```

Coverage includes path traversal, symlink escapes, cross-share isolation, resume conflicts, `Range` downloads, read-only mode and the translation catalogue.

The generated Wails bindings under `frontend/bindings` are committed so a fresh
clone builds without the CLI. After changing an exported Go method, run
`wails3 generate bindings -ts` and commit the result.

### Versioning

`build/config.yml` → `info.version` is the single source of truth. Every other
version string (plist, Windows metadata, MSIX manifests, deb config, npm
packages) is derived from it:

```bash
wails3 task version:sync VERSION=1.2.3  # set the version repo-wide
wails3 task version:check               # verify all files agree
```

Never edit the generated files by hand — `version:sync` overwrites them. CI
runs `version:check` on every push. Pushing a `v*` tag syncs all versions from
that tag before building, so artifacts always match the release they ship under.

Release notes live in `.github/release-notes/<tag>.md` (for example
`v0.2.0.md`). A version without a note there falls back to GitHub's
auto-generated release notes.

## Roadmap

- [ ] Post-transfer verification (size plus optional hash)
- [ ] Per-share read-only / read-write toggle
- [ ] Optional access password
- [ ] Persistent transfer history

## License

[MIT](LICENSE)
