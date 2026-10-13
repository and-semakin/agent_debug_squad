//go:build darwin || linux

package devin

import (
	"context"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOwnedCleanupWithDescriptorHoldingChild(t *testing.T) {
	unrelated := exec.Command("sleep", "30")
	if e := unrelated.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { unrelated.Process.Kill(); unrelated.Wait() }()
	dir := t.TempDir()
	script := filepath.Join(dir, "child")
	os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 &\necho $! > child.pid\nexit 0\n"), 0700)
	cmd := exec.Command(script)
	cmd.Dir = dir
	c, e := startClient(cmd)
	if e != nil {
		t.Fatal(e)
	}
	var pid int
	for i := 0; i < 100; i++ {
		b, _ := os.ReadFile(filepath.Join(dir, "child.pid"))
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		if pid > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		c.close()
		t.Fatal("no child")
	}
	start := time.Now()
	if e = c.close(); e != nil || time.Since(start) > 5*time.Second {
		t.Fatal("cleanup failed", e)
	}
	if e = unrelated.Process.Signal(syscall.Signal(0)); e != nil {
		t.Fatal("unrelated process killed", e)
	}
	// A reaped child is absent; some Linux init processes retain a killed zombie.
	for i := 0; i < 100; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		if b, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); e == nil && strings.Contains(string(b), ") Z ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("owned child survived cleanup")
}
func TestPreparationCancellationAndExpiry(t *testing.T) {
	a, s := fixtureAdapter(t, "ok")
	ctx, cancel := context.WithCancel(context.Background())
	s, e := a.Init(ctx, a.spec, s)
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	for i := 0; i < 100; i++ {
		a.mu.Lock()
		p := a.prepared
		a.mu.Unlock()
		if p == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, _, e = a.Send(context.Background(), s, domain.RunRequest{Message: "never"}, nil)
	if e == nil {
		t.Fatal("lost empty session silently recreated")
	}
	if len(calls(t, s.WorkspaceDir, "session/prompt")) != 0 {
		t.Fatal("cancelled preparation prompted")
	}
}

func TestForcedTeardownReportsUncertainty(t *testing.T) {
	c, e := startClient(exec.Command("sleep", "30"))
	if e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	e = c.close()
	if e == nil || time.Since(start) > 5*time.Second {
		t.Fatal("forced cleanup incorrectly reported success", e)
	}
}
