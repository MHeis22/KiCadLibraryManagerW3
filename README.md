# KiCad Library Manager

[![GitHub Stars](https://img.shields.io/github/stars/MHeis22/KiCadLibraryManager?style=flat&logo=github&label=Stars)](https://github.com/MHeis22/KiCadLibraryManager/stargazers)
[![GitHub Downloads](https://img.shields.io/github/downloads/MHeis22/KiCadLibraryManager/total?style=flat&logo=github&label=Downloads)](https://github.com/MHeis22/KiCadLibraryManager/releases/latest)

[**⬇ Download Latest Release**](https://github.com/MHeis22/KiCadLibraryManager/releases/latest)

A system tray app that automates importing KiCad components into your personal library — symbols, footprints, 3D models, and design blocks — with one click.

Built with [Go](https://go.dev/) and [Wails v3](https://v3.wails.io/).

---

## The Problem

Adding a downloaded component to KiCad involves many manual steps:

1. Unzip the downloaded asset package
2. Move each file type to its specific library folder
3. Manually patch 3D model paths inside `.kicad_mod` footprint files
4. Append symbol definitions into the correct `.kicad_sym` library file
5. Register each new library in KiCad's `sym-lib-table` and `fp-lib-table`

**This tool reduces all of that to a single confirmation click.**

---

## Features

- **Watch folder integration** — monitors a folder (e.g. Downloads) for new KiCad-compatible files; automatically surfaces the UI when something is detected
- **Drag and drop** — drag `.zip`, `.epw`, or individual KiCad files/folders directly onto the app window
- **Auto-categorisation** — reads `ki_description` and `ki_keywords` from symbol files to suggest the right library category
- **Conflict detection** — checks for duplicate symbols, footprints, and 3D models before importing, with skip/overwrite/rename options
- **Multi-repository support** — organise your library into multiple named repositories; optionally back each one with a remote Git URL
- **Git sync** — auto-pulls before import and pushes after, with push-rejection retry logic (up to 3 attempts)
- **Undo** — reverts the last import by removing added files and restoring the symbol library backup
- **Import history** — keeps the last 10 imports for reference and undo
- **Auto-start on login** — optional 1-click system startup registration (Windows)
- **macOS tray behaviour** — runs as an Accessory app (no Dock icon) to stay out of the way

### Supported file formats

| Type | Extensions |
|---|---|
| Symbols | `.kicad_sym` |
| Footprints | `.kicad_mod` |
| 3D models | `.step`, `.stp`, `.wrl` |
| Design blocks | `.kicad_sch`, `.kicad_pcb` |
| Archives | `.zip`, `.epw` |

---

## Getting Started

### First-run setup

1. The settings window opens automatically on first launch.
2. Set your **Base Library Path** — the root folder where all library subfolders will be created.
3. Set your **Watch Directory** — the folder to monitor for new files (defaults to `~/Downloads`).
4. Optionally add one or more named repositories. You can link each to a remote Git URL for syncing across machines.

The app registers itself with KiCad's `sym-lib-table` and `fp-lib-table` automatically after you save settings. No manual KiCad configuration needed.

---

## How It Works

1. The app watches your chosen directory using `fsnotify`.
2. When a `.zip` or `.epw` file appears, it waits for the file to finish downloading (polls size stability + checks write-lock).
3. It peeks inside the archive to confirm it contains KiCad files before surfacing the UI.
4. The UI shows what was detected and suggests a category based on the component's embedded description and keywords.
5. On confirmation, the app:
   - Extracts the archive to a temp directory
   - Copies each file type to the correct subfolder inside your library
   - Patches `${KICAD_USER_LIBRARY}` path references in footprint files
   - Appends the symbol to the category's `.kicad_sym` master file (backing up first)
   - Commits and pushes if the target repository if Git-backed
6. A history record is saved, enabling one-click undo.

---

## Configuration

Settings are stored at:

| Platform | Path |
|---|---|
| macOS / Linux | `~/.config/KiCadLibMgr/config.json` |
| Windows | `%APPDATA%\KiCadLibMgr\config.json` |

The config file is managed automatically by the app. Direct edits are possible but not required.

### Categories

Custom categories can be added, renamed, or deleted from within the app. New categories are immediately registered as KiCad library entries. If a new category is added, KiCad must be restarted.

---

## Safety

- Imports are non-destructive by default — existing files are not overwritten without confirmation.
- Symbol library files are backed up before modification; undo restores from the backup.
- Removing a repository from the app only unlinks it from the config; no files are deleted from disk.
- The watcher ignores zero-byte browser placeholder files and non-KiCad archives.

---

## Status

This project is functional but under active development. Core import, conflict detection, undo, and Git sync features work end-to-end. Edge cases and Linux are still being tested and stabilised.
