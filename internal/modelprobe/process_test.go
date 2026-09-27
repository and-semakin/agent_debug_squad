//go:build darwin || linux

package modelprobe

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectLimitsAndDescendantCancellation(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "probe")
	if e := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 &\nwait\n"), 0700); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e := Collect(ctx, script, nil, []string{"PATH=/bin:/usr/bin"}, dir)
	if e == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("%v %v", e, time.Since(start))
	}
	if e := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'hello\\n'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	b, e := Collect(ctx2, script, nil, []string{}, dir)
	if e != nil || strings.TrimSpace(string(b)) != "hello" {
		t.Fatalf("%s %v", b, e)
	}
}

func TestReadLimits(t *testing.T) {
	for _, size := range []int{MaxFrame + 1, MaxBytes + 1} {
		input := strings.Repeat("x", size)
		if size > MaxFrame+1 {
			input = strings.Repeat(strings.Repeat("x", 1023)+"\n", size/1024+1)
		}
		scanner := bufio.NewScanner(strings.NewReader(input))
		scanner.Buffer(make([]byte, 4096), MaxFrame+1)
		p := Process{Out: scanner}
		for {
			_, e := p.Read()
			if e == ErrLimit {
				break
			}
			if e != nil {
				t.Fatalf("wanted limit, got %v", e)
			}
		}
	}
}

func TestSafeCommandFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "failure")
	for text, want := range map[string]string{"unauthorized SECRET_SENTINEL": "auth_required", "unknown command SECRET_SENTINEL": "unsupported", "SECRET_SENTINEL": "error"} {
		if e := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '"+text+"' >&2\nexit 1\n"), 0700); e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, e := Collect(ctx, script, nil, []string{}, dir)
		cancel()
		if CommandStatus(context.Background(), e) != want || strings.Contains(e.Error(), "SECRET_SENTINEL") {
			t.Fatal(e, want)
		}
	}
}
