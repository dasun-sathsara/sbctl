// Package assets embeds the files sbctl ships with.
//
// The skeleton stays at assets/skeleton.json rather than moving under internal/
// so that it remains the single canonical seed profile: the binary embeds this
// exact file, and the install scripts can read the same one. Duplicating it
// would let the two drift, and a stale copy of the template is a profile whose
// placeholder guard no longer matches.
package assets

import _ "embed"

// Skeleton is the seed profile used by `sbctl add`. It intentionally contains
// TODO_ placeholders, which sbctl refuses to activate until they are replaced.
//
//go:embed skeleton.json
var Skeleton string
