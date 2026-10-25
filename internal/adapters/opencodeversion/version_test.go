package opencodeversion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionMajorAndUnrecognizedOutput(t *testing.T) {
	for _, tc := range []struct {
		output string
		major  int
		ok     bool
	}{{"1.18.30", 1, true}, {"opencode v2.1.4", 2, true}, {"opencode v2.1.4", 1, false}, {"1.18.30", 2, false}, {"not a version", 2, false}} {
		dir := t.TempDir()
		command := filepath.Join(dir, "opencode")
		if err := os.WriteFile(command, []byte("#!/bin/sh\necho '"+tc.output+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		err := Check(context.Background(), command, []string{}, dir, tc.major)
		if (err == nil) != tc.ok {
			t.Fatalf("%+v %v", tc, err)
		}
	}
}
