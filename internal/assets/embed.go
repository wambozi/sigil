// Package assets embeds static files (shell hooks, service units) into the
// binary so that "sigild init" works without requiring the source tree.
package assets

import "embed"

//go:embed scripts/shell-hook.zsh scripts/shell-hook.bash deploy/sigild.service
var FS embed.FS
