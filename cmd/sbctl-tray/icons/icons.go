package icons

import _ "embed"

// Active is the green tray icon shown when the proxy is running.
//
//go:embed active.png
var Active []byte

// Inactive is the gray tray icon shown when the proxy is stopped.
//
//go:embed inactive.png
var Inactive []byte

// Error is the red tray icon shown when there's a daemon error.
//
//go:embed error.png
var Error []byte
