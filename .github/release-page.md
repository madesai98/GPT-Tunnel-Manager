# GPT Tunnel Manager v1.0.24

## Copyable one-line app descriptions

This release makes the per-server Developer Plugin app description directly copyable from the native UI.

- Renames the server `Marker` and editor `Lifecycle Marker` buttons to `Copy App Description`.
- Clicking `Copy App Description` now writes the generated description directly to the system clipboard instead of only showing it in the status message.
- Changes the generated description to the single-line format `GTM PLUGIN | srv_... | Follow the gpt-tunnel-manager-lifecycle skill before using this plugin` so it works in app-description fields that do not preserve newlines.
- Updates marker parsing and tests for the new one-line format.
- Updates both the installable lifecycle skill and the compiled packaged skill copy to recognize the new `GTM PLUGIN` format.
- Updates README, architecture context, ADR 0004, and the implementation contract to document the one-line app description.

## Included builds

- Windows x64
- Windows ARM64
- Linux x64
- Linux ARM64
- macOS Intel x64
- macOS Apple Silicon ARM64
- Source archives
- `SHA256SUMS.txt`

## Changes since v1.0.23

- Copy Developer Plugin app descriptions directly to the clipboard.
- Replace the old multiline lifecycle marker with the condensed one-line `GTM PLUGIN` description.
- Keep the lifecycle skill and packaged documentation aligned with the new description format.
