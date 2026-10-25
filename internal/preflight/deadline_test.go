package preflight

import (
	"context"
	"testing"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestServiceDeadlineDoesNotUseInstallationLimit(t *testing.T) {
	for _, tc := range []struct {
		name          string
		timeout, want time.Duration
	}{
		{"installation", 0, 5 * time.Second},
		{"service", 60 * time.Second, 60 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var remaining time.Duration
			report := Pass(context.Background(), 90*time.Second, []Entry{{Backend: "opencode2", Timeout: tc.timeout, Check: func(ctx context.Context) domain.InstallationResult {
				deadline, _ := ctx.Deadline()
				remaining = time.Until(deadline)
				return domain.InstallationResult{Status: domain.InstallationStatusReady}
			}}})
			if len(report.Issues) != 0 || remaining < tc.want-time.Second || remaining > tc.want {
				t.Fatalf("remaining=%s, want approximately %s; report=%+v", remaining, tc.want, report)
			}
		})
	}
}
