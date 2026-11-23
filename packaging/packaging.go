// Package packaging holds the desktop app's icon and installer sources.
package packaging

import _ "embed"

// Icon is the application icon (512×512 PNG), shown by the Linux window.
//
//go:embed icon.png
var Icon []byte
