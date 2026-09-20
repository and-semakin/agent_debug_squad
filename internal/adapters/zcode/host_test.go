package zcode

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func requireNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node not installed; protocol tests do not require it")
	}
	return node
}

func TestHostGuardAndRedaction(t *testing.T) {
	node := requireNode(t)
	tmp := t.TempDir()
	host := filepath.Join(tmp, "host.cjs")
	unknown := filepath.Join(tmp, "unknown.cjs")
	if err := os.WriteFile(host, []byte(hostSource), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unknown, []byte("throw Error('must not execute');"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", hostSource, unknown)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "Unsupported ZCode runtime") || !strings.Contains(string(out), "CLI autorun") || strings.Contains(string(out), "must not execute") {
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

// validFixture carries one copy of every anchor the probe matches on: the CLI
// autorun statement, the credentials.json path builder and the store factory
// built on it, the *RegistryRuntime keep-name class with its construction site,
// and the two module init thunks owning the keep-name registrations.
const validFixture = `q9t();vtc();async function vtc(){}
function credPath(e={}){return join(home(e),".zcode","v2","credentials.json")}
function storeFactory(e={}){let t=e.env??process.env,n=credPath(e);return{filePath:n,async load(s){return n}}}
Kat=Y(()=>{pUs="ZCODE_DATA_BASE_DIR";r(storeFactory,"createSharedZCodeCredentialStore")})
RC=class{static{r(this,"NodeProviderRegistryRuntime")}}
function registryFactory(e){return new RC(e)}
ukt=Y(()=>{ckt=require("node:path");r(registryFactory,"startProcessProviderRegistryRuntime")})`

func discoverFixture(t *testing.T, node, host, bundle string) string {
	t.Helper()
	script := `const h=require(process.argv[1]),fs=require('node:fs');try{process.stdout.write(JSON.stringify(h.discover(fs.readFileSync(process.argv[2],'utf8'))))}catch(e){process.stdout.write('discovery failed: '+e.message);process.exit(3)}`
	out, err := exec.Command(node, "-e", script, host, bundle).CombinedOutput()
	if err != nil {
		t.Fatalf("discover %s: %s %v", bundle, out, err)
	}
	return string(out)
}

func TestHostDiscovery(t *testing.T) {
	node := requireNode(t)
	tmp := t.TempDir()
	host := filepath.Join(tmp, "host.cjs")
	if err := os.WriteFile(host, []byte(hostSource), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(tmp, "bundle.cjs")
	if err := os.WriteFile(fixture, []byte(validFixture), 0600); err != nil {
		t.Fatal(err)
	}
	out := discoverFixture(t, node, host, fixture)
	var found struct {
		Hash        string   `json:"hash"`
		Patched     string   `json:"patched"`
		Credentials string   `json:"credentials"`
		Registry    string   `json:"registry"`
		Thunks      []string `json:"thunks"`
	}
	if err := json.Unmarshal([]byte(out), &found); err != nil {
		t.Fatalf("discovery output: %s %v", out, err)
	}
	if found.Credentials != "storeFactory" || found.Registry != "registryFactory" {
		t.Fatalf("wrong exports: %s", out)
	}
	if len(found.Thunks) != 2 || found.Thunks[0] != "Kat" || found.Thunks[1] != "ukt" {
		t.Fatalf("wrong module thunks: %s", out)
	}
	if !strings.Contains(found.Patched, "q9t();async function vtc()") || strings.Contains(found.Patched, "vtc();async function") {
		t.Fatalf("autorun call not stripped: %s", found.Patched)
	}

	unsupported := regexp.MustCompile(`Unsupported ZCode runtime [0-9a-f]{64}: .*anchor`)
	broken := filepath.Join(tmp, "broken.cjs")
	noAutorun := strings.Replace(validFixture, "q9t();vtc();async function vtc(){}\n", "", 1)
	if err := os.WriteFile(broken, []byte(noAutorun), 0600); err != nil {
		t.Fatal(err)
	}
	out = discoverFailure(t, node, host, broken)
	if !unsupported.MatchString(out) || !strings.Contains(out, "CLI autorun anchor: 0 matches") {
		t.Fatalf("missing autorun: %s", out)
	}
	ambiguous := strings.Replace(validFixture, "q9t();vtc();async function vtc(){}\n",
		"a1();b1();async function b1(){}\na2();b2();async function b2(){}\n", 1)
	if err := os.WriteFile(broken, []byte(ambiguous), 0600); err != nil {
		t.Fatal(err)
	}
	out = discoverFailure(t, node, host, broken)
	if !unsupported.MatchString(out) || !strings.Contains(out, "CLI autorun anchor: 2 matches") {
		t.Fatalf("ambiguous autorun: %s", out)
	}
}

func discoverFailure(t *testing.T, node, host, bundle string) string {
	t.Helper()
	script := `const h=require(process.argv[1]),fs=require('node:fs');try{h.discover(fs.readFileSync(process.argv[2],'utf8'));process.exit(4)}catch(e){process.stdout.write('discovery failed: '+e.message);process.exit(3)}`
	out, err := exec.Command(node, "-e", script, host, bundle).CombinedOutput()
	if err == nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("expected discovery failure for %s: %s %v", bundle, out, err)
	}
	return string(out)
}
