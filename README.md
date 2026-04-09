# KiCad Library Manager

Built with Go and Wails

A system tray app which manages kicad symbol, design block, footprint and 3D files. Everything is not yet tested but it wont break your KiCad.

Makes neat subfolders inside a user chosen folder by component category. Watches a user specified folder for files, folders and zip files with kicad valid files. Automatically shows UI and tells the user what kind of files it detected and what the user wants to do with them. Supports drag and drop.

## Why

1. Manually adding parts to KiCad is a multi-step process involving:
2. Unzipping downloaded assets.
3. Moving files to specific library folders.
4. Manually patching 3D model paths within footprint files.
5. Appending symbol definitions to .kicad_sym library files.
6. Registering new libraries in KiCad's global sym-lib-table and fp-lib-table.

### This tool reduces these steps to a single confirmation click.
