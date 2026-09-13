// Package migrations embeds the schema shipped with this application version.
package migrations

import "embed"

// Files contains forward migrations. Rollbacks remain an explicit operator task.
//
//go:embed *.up.sql
var Files embed.FS
