package zcode

import (
	"context"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCatalogDoesNotLoadRuntime(t *testing.T) {
	dir := t.TempDir()
	runtime := filepath.Join(dir, "runtime.cjs")
	if e := os.WriteFile(runtime, []byte(`require('fs').writeFileSync('unexpected','loaded');throw Error('must not load');`), 0600); e != nil {
		t.Fatal(e)
	}
	a := New(domain.AgentSpec{StringOptions: map[string]string{"runtime_path": runtime}})
	r := a.ListModels(context.Background(), domain.ModelDiscoveryInput{WorkspaceDir: dir, AmbientEnv: []string{"HOME=" + dir}})
	if r.Status != "unsupported" || r.Diagnostics[0].Code != "read_only_catalog_unavailable" {
		t.Fatal(r)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal(entries)
	}
}
func TestCatalogProbeUnderWriteDenial(t *testing.T) {
	node := requireNode(t)
	dir := t.TempDir()
	host := filepath.Join(dir, "host.cjs")
	bundle := filepath.Join(dir, "fixture.cjs")
	if e := os.WriteFile(host, []byte(hostSource), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(bundle, []byte(validFixture), 0600); e != nil {
		t.Fatal(e)
	}
	script := `const fs=require('node:fs'),h=require(process.argv[1]);for(const k of ['writeFileSync','appendFileSync','mkdirSync','renameSync','unlinkSync'])fs[k]=()=>{throw Error('persistent write denied')};const found=h.discover(fs.readFileSync(process.argv[2],'utf8'));if(!found.registry)process.exit(3);if(typeof h.listModels==='function')process.exit(4);process.stdout.write('structural-only; no verified catalog bootstrap');`
	cmd := exec.Command(node, "-e", script, host, bundle)
	cmd.Env = []string{"HOME=" + dir}
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("%v %s", e, b)
	}
}
