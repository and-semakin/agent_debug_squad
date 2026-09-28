package zcode

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
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
ukt=Y(()=>{ckt=require("node:path");r(registryFactory,"startProcessProviderRegistryRuntime")})
function snapParser(e){return {revision:e.revision}}
Zq=Y(()=>{r(snapParser,"parseProcessAccountProviderConfigSnapshot")})`

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
	if len(found.Thunks) != 3 || found.Thunks[0] != "Kat" || found.Thunks[1] != "ukt" || found.Thunks[2] != "Zq" {
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

// b64url encodes a JWT segment the way issuers do.
func b64url(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func jwt(t *testing.T, payload map[string]any) string {
	t.Helper()
	return "eyJhbGciOiJIUzI1NiJ9." + b64url(t, payload) + ".c2ln"
}

func TestHostIdentityBinding(t *testing.T) {
	node := requireNode(t)
	tmp := t.TempDir()
	host := filepath.Join(tmp, "host.cjs")
	if err := os.WriteFile(host, []byte(hostSource), 0600); err != nil {
		t.Fatal(err)
	}
	individualKey := "account-provider:coding-plan:account:zai-individual-coding-plan:account:user-42:api-key"
	script := `const h=require(process.argv[1]);
const [policy, token, keys] = [process.argv[2], process.argv[3], JSON.parse(process.argv[4])];
const identity = h.resolveIdentity(policy, keys, token);
process.stdout.write(JSON.stringify(identity));`
	run := func(policy, token string, keys []string) map[string]any {
		out, err := exec.Command(node, "-e", script, host, policy, token, mustJSON(t, keys)).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v", out, err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(out, &parsed); err != nil {
			t.Fatalf("%s %v", out, err)
		}
		return parsed
	}

	current := jwt(t, map[string]any{"sub": "user-42", "exp": float64(time.Now().Add(time.Hour).Unix())})
	expired := jwt(t, map[string]any{"sub": "user-42", "exp": float64(time.Now().Add(-time.Hour).Unix())})
	foreign := jwt(t, map[string]any{"sub": "user-43", "exp": float64(time.Now().Add(time.Hour).Unix())})

	cases := []struct {
		name          string
		policy        string
		token         string
		keys          []string
		ok            bool
		identityMatch bool
	}{
		{"fixed unique key", "fixed", "", []string{individualKey}, true, false},
		{"fixed multiple keys rejected", "fixed", "", []string{individualKey, individualKey}, false, false},
		{"start-first bound identity", "start-first", current, []string{individualKey}, true, true},
		{"start-first foreign identity", "start-first", foreign, []string{individualKey}, false, false},
		{"start-first expired token", "start-first", expired, []string{individualKey}, false, false},
		{"start-first missing token", "start-first", "", []string{individualKey}, false, false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			parsed := run(item.policy, item.token, item.keys)
			if parsed["ok"] != item.ok || parsed["identityMatch"] != item.identityMatch {
				t.Fatalf("%v", parsed)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestHostGuardedReadHelpers(t *testing.T) {
	node := requireNode(t)
	tmp := t.TempDir()
	host := filepath.Join(tmp, "host.cjs")
	if err := os.WriteFile(host, []byte(hostSource), 0600); err != nil {
		t.Fatal(err)
	}
	script := `const h=require(process.argv[1]);
const out = {
  noProxyExact: h.noProxyMatches('api.z.ai', 'api.z.ai,example.com'),
  noProxySuffix: h.noProxyMatches('sub.api.z.ai', '.api.z.ai'),
  noProxyMiss: h.noProxyMatches('api.z.ai', 'example.com,.bigmodel.cn'),
  noProxyWildcard: h.noProxyMatches('api.z.ai', '*'),
  noProxyEmpty: h.noProxyMatches('api.z.ai', ''),
  keyBare: h.normalizeApiKeyForHeader('abc12345.xyz98765'),
  keyBearer: h.normalizeApiKeyForHeader('Bearer abc12345.xyz98765'),
  keyPadded: h.normalizeApiKeyForHeader('  abc12345.xyz98765 notes'),
  envOk: h.readSuccessfulEnvelope({code: 200, success: true, data: {a: 1}}).ok,
  envOmittedCode: h.readSuccessfulEnvelope({data: []}).ok,
  envZero: h.readSuccessfulEnvelope({code: 0, data: []}).ok,
  envSuccessFalse: h.readSuccessfulEnvelope({success: false, code: 200, data: {x: 1}}).ok,
  envBusinessError: h.readSuccessfulEnvelope({success: true, code: 500, data: {y: 2}}).ok,
  envNonObject: h.readSuccessfulEnvelope('nope').ok,
};
process.stdout.write(JSON.stringify(out));`
	out, err := exec.Command(node, "-e", script, host).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v", out, err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	expect := map[string]any{
		"noProxyExact": true, "noProxySuffix": true, "noProxyMiss": false,
		"noProxyWildcard": true, "noProxyEmpty": false,
		"keyBare": "abc12345.xyz98765", "keyBearer": "abc12345.xyz98765", "keyPadded": "abc12345.xyz98765",
		"envOk": true, "envOmittedCode": true, "envZero": true,
		"envSuccessFalse": false, "envBusinessError": false, "envNonObject": false,
	}
	for key, want := range expect {
		if parsed[key] != want {
			t.Fatalf("%s = %v, want %v", key, parsed[key], want)
		}
	}
}

// A proxy that answers CONNECT with 200 and then speaks plaintext HTTP used to
// receive the Authorization header in the clear and have its fake response
// accepted. With TLS inside the tunnel the read must fail instead.
func TestHostProxiedReadRejectsPlaintextTunnel(t *testing.T) {
	node := requireNode(t)
	tmp := t.TempDir()
	host := filepath.Join(tmp, "host.cjs")
	if err := os.WriteFile(host, []byte(hostSource), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("cannot hijack")
				return
			}
			conn, buf, err := hijacker.Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			buf.Flush()
			// Plaintext HTTP where TLS is expected: the read must fail.
			buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 15\r\n\r\n{\"stolen\":true}")
			buf.Flush()
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})}
	go server.Serve(listener)
	defer func() { _ = server.Close() }()

	script := `const h=require(process.argv[1]);
process.env.ZCODE_HTTP_PROXY=process.argv[2];
h.performRead('https://api.z.ai/api/biz/subscription/list','synthetic-secret').then(r=>process.stdout.write(JSON.stringify(r)));`
	cmd := exec.Command(node, "-e", script, host, "http://"+listener.Addr().String())
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v", out, err)
	}
	var response map[string]any
	if err := json.Unmarshal(out, &response); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if response["ok"] == true {
		t.Fatalf("plaintext tunnel accepted: %s", out)
	}
	if strings.Contains(string(out), "synthetic-secret") {
		t.Fatal("the authorization header leaked into the diagnostic")
	}
}

func TestHostProviderHeaderRequestExtraction(t *testing.T) {
	node := requireNode(t)
	tmp := t.TempDir()
	host := filepath.Join(tmp, "host.cjs")
	if err := os.WriteFile(host, []byte(hostSource), 0600); err != nil {
		t.Fatal(err)
	}
	// The wire shape from the inspected source: the model rides in
	// params.modelSelection, and there is no top-level modelId.
	script := `const h=require(process.argv[1]);
process.stdout.write(JSON.stringify([
  h.providerHeaderRequest({requestId:'r1', sessionId:'s1', turnId:'t1', providerId:'account:zai-individual-coding-plan', modelSelection:{providerId:'account:zai-individual-coding-plan', modelId:'GLM-5.3-Flash'}, reason:'model-request'}),
  h.providerHeaderRequest({providerId:'p', modelId:'WRONG'}),
  h.providerHeaderRequest(undefined),
]));`
	out, err := exec.Command(node, "-e", script, host).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v", out, err)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if parsed[0]["modelId"] != "GLM-5.3-Flash" || parsed[0]["sessionId"] != "s1" || parsed[0]["reason"] != "model-request" {
		t.Fatalf("wire shape lost: %v", parsed[0])
	}
	if parsed[1]["modelId"] != "" {
		t.Fatalf("top-level modelId must be ignored: %v", parsed[1])
	}
	if parsed[2]["providerId"] != "" {
		t.Fatalf("missing params must degrade safely: %v", parsed[2])
	}
}
