# Bulk File Renamer

Rename hundreds of files at once, entirely on this machine: chain together
find/replace, regex, sequence numbering, date-stamping, case, trim, and
extension rules, watch a live preview update as you build the chain, and
only then touch disk. Opens as its own window.

No accounts, no upload, nothing sent anywhere — every rename happens on
files already on your computer.

## Requirements

**A Chromium-based browser already installed**: Google Chrome, Chromium,
Brave, Microsoft Edge, or Arc. Bulk File Renamer renders its own UI inside
it; it doesn't install or bundle a browser itself. If none is found, it
tells you on launch instead of failing silently.

**Linux only**: a native folder-picker — `zenity` (GNOME default) or
`kdialog` (KDE default) — since a browser page can't hand back a real
filesystem path on its own. macOS and Windows use their built-in pickers;
nothing extra to install there.

## Use

1. Open Bulk File Renamer — it opens its own window.
2. **Choose a folder to rename files in.** Toggle "Include subfolders" if
   you want a recursive listing; otherwise only files directly inside the
   folder are listed. Every file starts checked — uncheck any you want to
   leave alone.
3. **Add rules** — find & replace, regex replace, sequence number, date,
   change case, trim characters, remove characters, or change extension.
   Add as many as you like and reorder them with the ↑/↓ controls; rules
   apply in order, top to bottom, to each file's name (never its
   extension, unless you add a "change extension" rule).
4. Watch the **live preview** update as you edit rules or your selection.
   Any row that would collide with another file's new name — or an
   existing file it isn't part of this batch — turns red; a banner tells
   you how many collisions need resolving before you can proceed.
5. Click **Rename files** once the preview looks right.
6. If anything looks wrong afterward, click **Undo** — it reverses the
   exact batch you just applied, renaming everything back to what it was
   before. Nothing is ever deleted by this app, only renamed.

Renames are two-phase and crash-safe under the hood: every file in a batch
first moves to a temporary name, then to its final name, so overlapping
renames — even a straight two-file swap — can never clobber or lose a file
partway through. If any step in a batch fails, the whole batch rolls back
so every file ends up exactly where it started.

## License

MIT — see [LICENSE](LICENSE).
