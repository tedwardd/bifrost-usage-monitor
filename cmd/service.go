package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
)

// serviceProgram returns the executable path to record in the agent, preferring
// a stable PATH entry over the resolved binary so an upgrade does not leave the
// agent pointing into an old versioned directory.
func serviceProgram() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	if linked, err := exec.LookPath(filepath.Base(resolved)); err == nil {
		if abs, err := filepath.Abs(linked); err == nil && abs != resolved {
			if same, err := filepath.EvalSymlinks(abs); err == nil && same == resolved {
				return abs, nil
			}
		}
	}
	return resolved, nil
}
