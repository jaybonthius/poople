package unlimited

import "embed"

// StaticDirectory holds the bundled unlimited stylesheet; run the bundle task
// to build it.
//
//go:embed static
var StaticDirectory embed.FS
