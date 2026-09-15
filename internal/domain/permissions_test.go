package domain

import (
	"encoding/json"
	"testing"
)

func TestPermissionSnapshotOwnsNestedValues(t *testing.T) {
	original := []PermissionRequest{{ID: "per_1", Patterns: []string{"/tmp/*"}, Always: []string{"/tmp/*"}, Metadata: json.RawMessage(`{"path":"/tmp/file"}`), Tool: json.RawMessage(`{"callID":"a"}`)}}
	cloned := ClonePermissions(original)
	cloned[0].Patterns[0] = "changed"
	cloned[0].Always[0] = "changed"
	cloned[0].Metadata[0] = '!'
	cloned[0].Tool[0] = '!'
	if original[0].Patterns[0] != "/tmp/*" || original[0].Always[0] != "/tmp/*" || !json.Valid(original[0].Metadata) || !json.Valid(original[0].Tool) {
		t.Fatal("snapshot aliases original")
	}
	raw, err := json.Marshal(RunProgress{Phase: RunPhaseWaitingForPermission, PendingPermissions: original})
	if err != nil {
		t.Fatal(err)
	}
	var restored RunProgress
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.PendingPermissions) != 1 || !restored.NeedsPermissionReply() {
		t.Fatalf("restored=%+v", restored)
	}
}
