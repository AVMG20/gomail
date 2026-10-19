// Package appicon embeds the application icon (used for the system tray).
package appicon

import _ "embed"

// PNG is the app icon.
//
//go:embed icon.png
var PNG []byte
