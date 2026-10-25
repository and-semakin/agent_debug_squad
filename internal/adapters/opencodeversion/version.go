// Package opencodeversion distinguishes incompatible CLI generations before startup.
package opencodeversion

import (
	"context"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
	"strings"
	"time"
)

func Check(ctx context.Context, command string, env []string, workspace string, major int) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := modelprobe.Collect(ctx, command, []string{"--version"}, env, workspace)
	version := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "opencode "))
	version = strings.TrimPrefix(version, "v")
	if err != nil || !strings.HasPrefix(version, fmt.Sprintf("%d.", major)) || strings.ContainsAny(version, "\r\n \t") {
		return fmt.Errorf("OpenCode %d executable required; select opencode1/opencode2 or configure the matching command path", major)
	}
	return nil
}
