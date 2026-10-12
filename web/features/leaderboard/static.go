package leaderboard

import "embed"

// StaticDirectory holds the bundled leaderboard stylesheet; run the bundle
// task to build it.
//
//go:embed static
var StaticDirectory embed.FS
