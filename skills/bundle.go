// Package skills contains the canonical operating skill shipped in every binary.
package skills

import (
	"embed"
	"encoding/json"
	"io/fs"
)

const Name = "agent-debug-squad"

//go:embed all:agent-debug-squad
var files embed.FS

// executable-files.json preserves executable intent, which embed.FS does not.
// Source validation checks it against the actual source files before release.
//
//go:embed executable-files.json
var executableFiles []byte

func Files() fs.FS { sub, _ := fs.Sub(files, Name); return sub }
func Executables() ([]string, error) {
	var names []string
	err := json.Unmarshal(executableFiles, &names)
	return names, err
}
