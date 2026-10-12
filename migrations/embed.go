package migrations

import "embed"

// FS is the single migration source used by the binary, Docker, and reviewers.
//
//go:embed *.sql
var FS embed.FS
