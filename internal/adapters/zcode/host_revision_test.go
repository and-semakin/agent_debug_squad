package zcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the embedded JavaScript host itself, not the Go protocol helper,
// against independently checked native-shaped registry and wire contracts.
func TestHostAccountRevisionContract(t *testing.T) {
	node := requireNode(t)
	bundle, err := filepath.Abs("testdata/host/account-revision.cjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"registry", "overlay", "reject-registry", "reject-overlay"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".zcode", "v2")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			// Only the key identity is read; the fixture store supplies a fake secret.
			if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(`{"account-provider:coding-plan:account:zai-individual-coding-plan:account:fixture:api-key":{}}`), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(node, "-e", hostSource, bundle)
			cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "SQUAD_FIXTURE_REJECT=" + strings.TrimPrefix(scenario, "reject-")}
			if !strings.HasPrefix(scenario, "reject-") {
				cmd.Env[2] = "SQUAD_FIXTURE_REJECT="
			}
			c, err := startClient(cmd, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			defer c.close()
			c.setHandler(func(wireMessage) {})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var boot struct {
				OK      bool   `json:"ok"`
				Message string `json:"message"`
			}
			if err := c.call(ctx, "squad/bootstrap", map[string]any{"generation": 1, "workspace": home}, &boot); err != nil {
				t.Fatal(err)
			}
			if !boot.OK {
				t.Fatalf("bootstrap: %s", boot.Message)
			}
			request := overlayRequest{Provider: ProviderIndividual, Entitled: true, Current: true}
			if strings.HasSuffix(scenario, "registry") {
				outcome := (&hostBridge{call: c.call}).readRegistryView(ctx, request)
				if scenario == "reject-registry" {
					if outcome.ok || outcome.kind != readFailureSchema || outcome.failure != "The registry view could not be materialized; update the adapter." {
						t.Fatalf("unsafe rejection: %+v", outcome)
					}
					return
				}
				if !outcome.ok {
					t.Fatalf("registry snapshot rejected: %s", outcome.failure)
				}
				view, err := decodeRegistryView(outcome.payload)
				if err != nil {
					t.Fatal(err)
				}
				entry, ok := view.Models[registrySelectionKey(ProviderIndividual, "GLM-5.3-Flash")]
				if view.Revision != "native-fixture-revision" || !ok || !entry.supportsReasoning("low") {
					t.Fatalf("wrong registry projection: %+v", view)
				}
			} else {
				var result struct {
					Status   string `json:"status"`
					Revision string `json:"basedOnZCodeBuiltinRevision"`
				}
				err := c.call(ctx, "squad/applyAccountOverlay", request, &result)
				if scenario == "reject-overlay" {
					if err == nil || !strings.Contains(err.Error(), "fixture overlay rejected") {
						t.Fatalf("overlay rejection lost: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("native overlay rejected: %v", err)
				}
				if result.Status != "received" || result.Revision != "native-fixture-revision" {
					t.Fatalf("wrong overlay receipt: %+v", result)
				}
			}
		})
	}
}
