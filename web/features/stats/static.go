package stats

import "embed"

// StaticDirectory holds the bundled statistics stylesheet; run the bundle
// task to build it.
//
//go:embed static
var StaticDirectory embed.FS
