# App icon concepts

`D-envface.svg` is the current app icon (copied to `frontend/public/appicon.svg`). The others are
alternatives. `compare.png` shows all four at full size and at 64/32 px.

To switch icons, copy an SVG over `frontend/public/appicon.svg`, render it to a 1024×1024 PNG at
`build/appicon.png` (and a 256×256 PNG at `internal/appicon/icon.png` for the tray), then run
`wails3 task common:generate:icons` to regenerate the Windows `.ico` and macOS `.icns`.
