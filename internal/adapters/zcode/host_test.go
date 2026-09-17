package zcode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostGuardAndRedaction(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node not installed; protocol tests do not require it")
	}
	tmp := t.TempDir()
	host := filepath.Join(tmp, "host.cjs")
	unknown := filepath.Join(tmp, "unknown.cjs")
	if err = os.WriteFile(host, []byte(hostSource), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(unknown, []byte("throw Error('must not execute');"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", hostSource, unknown)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "Unsupported ZCode runtime fingerprint") || strings.Contains(string(out), "must not execute") {
		t.Fatalf("guard: %s %v", out, err)
	}
	script := `const h=require(process.argv[1]);const secret='sensitive"key';h.setSecretForTest(secret);process.stdout.write(h.safe(secret+' '+JSON.stringify({key:secret})));`
	out, err = exec.Command(node, "-e", script, host).CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "sensitive") || strings.Count(string(out), "[redacted]") != 2 {
		t.Fatalf("redaction failed: %s", out)
	}
}
