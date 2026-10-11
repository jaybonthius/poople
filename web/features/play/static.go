package play

import "embed"

// StaticDirectory holds the bundled play stylesheet and share component; run
// the bundle task to build it.
//
//go:embed static
var StaticDirectory embed.FS
