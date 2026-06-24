package research

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/perber/wiki/internal/core/tree"
)

func newTestService(t *testing.T, now time.Time) (*Service, string) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "schema.json"), []byte(fmt.Sprintf(`{"version":%d}`, tree.CurrentSchemaVersion)), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	treeSvc := tree.NewTreeService(tmp)
	if err := treeSvc.LoadTree(); err != nil {
		t.Fatalf("load tree: %v", err)
	}
	svc := NewService(Config{
		Tree: treeSvc,
		Now: func() time.Time {
			return now
		},
	})
	return svc, tmp
}

func TestCreateExperimentUsesReadableCanonicalIDAndWikiPath(t *testing.T) {
	svc, dir := newTestService(t, time.Date(2026, 6, 24, 4, 5, 6, 0, time.UTC))

	exp, err := svc.CreateExperiment(context.Background(), CreateExperimentInput{
		Project:   "DeltaKV",
		Title:     "Qwen3 KVzip SCBench SCDQ ratio 0.2",
		SlugHint:  "qwen3-kvzip-scdq-r02",
		Status:    "queued",
		Goal:      "Run the Qwen3 KVzip SCDQ baseline.",
		Command:   "bash scripts/tmp/run.sh",
		Model:     "Qwen3-4B-Instruct-2507",
		Method:    "KVzip",
		Benchmark: "SCBench",
		Tags:      []string{"scbench", "scdq"},
		Fingerprint: map[string]interface{}{
			"run_root": "/data2/outputs/run-a",
		},
	})
	if err != nil {
		t.Fatalf("CreateExperiment: %v", err)
	}

	wantID := "deltakv-20260624-qwen3-kvzip-scdq-r02"
	if exp.ID != wantID {
		t.Fatalf("id = %q, want %q", exp.ID, wantID)
	}
	wantPath := "projects/deltakv/experiments/2026/06/" + wantID
	if exp.Path != wantPath {
		t.Fatalf("path = %q, want %q", exp.Path, wantPath)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "root", "projects", "deltakv", "experiments", "2026", "06", wantID+".md"))
	if err != nil {
		t.Fatalf("read experiment file: %v", err)
	}
	content := string(raw)
	for _, needle := range []string{
		"research_id: " + wantID,
		"research_project: deltakv",
		"research_status: queued",
		"# Qwen3 KVzip SCBench SCDQ ratio 0.2",
		"Run the Qwen3 KVzip SCDQ baseline.",
		"bash scripts/tmp/run.sh",
	} {
		if !strings.Contains(content, needle) {
			t.Fatalf("expected content to contain %q:\n%s", needle, content)
		}
	}
}

func TestCreateExperimentReusesSameFingerprintAndSuffixesDifferentFingerprint(t *testing.T) {
	svc, _ := newTestService(t, time.Date(2026, 6, 24, 4, 5, 6, 0, time.UTC))
	input := CreateExperimentInput{
		Project:  "DeltaKV",
		Title:    "Qwen3 KVzip SCBench SCDQ ratio 0.2",
		SlugHint: "qwen3-kvzip-scdq-r02",
		Fingerprint: map[string]interface{}{
			"run_root": "/data2/outputs/run-a",
		},
	}

	first, err := svc.CreateExperiment(context.Background(), input)
	if err != nil {
		t.Fatalf("first CreateExperiment: %v", err)
	}
	second, err := svc.CreateExperiment(context.Background(), input)
	if err != nil {
		t.Fatalf("second CreateExperiment: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("same fingerprint id = %q, want %q", second.ID, first.ID)
	}
	if second.Created {
		t.Fatalf("same fingerprint should return existing experiment, not created=true")
	}

	input.Fingerprint = map[string]interface{}{"run_root": "/data2/outputs/run-b"}
	third, err := svc.CreateExperiment(context.Background(), input)
	if err != nil {
		t.Fatalf("third CreateExperiment: %v", err)
	}
	want := first.ID + "-02"
	if third.ID != want {
		t.Fatalf("different fingerprint id = %q, want %q", third.ID, want)
	}
}

func TestAppendEventAndStatusUpdateModifyExistingExperiment(t *testing.T) {
	now := time.Date(2026, 6, 24, 4, 5, 6, 0, time.UTC)
	svc, _ := newTestService(t, now)
	exp, err := svc.CreateExperiment(context.Background(), CreateExperimentInput{
		Project:  "DeltaKV",
		Title:    "Decode sweep",
		SlugHint: "decode-sweep",
		Status:   "queued",
	})
	if err != nil {
		t.Fatalf("CreateExperiment: %v", err)
	}

	_, err = svc.AppendEvent(context.Background(), AppendEventInput{
		ID:      exp.ID,
		Title:   "Queue started",
		Type:    "queue",
		Status:  "running",
		Content: "GPU wait loop started.",
		Metrics: map[string]interface{}{"expected_rows": 500},
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	updated, err := svc.UpdateStatus(context.Background(), UpdateStatusInput{
		ID:     exp.ID,
		Status: "completed",
		Note:   "All rows are present.",
	})
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if updated.Status != "completed" {
		t.Fatalf("status = %q, want completed", updated.Status)
	}
	got, err := svc.GetExperiment(context.Background(), exp.ID)
	if err != nil {
		t.Fatalf("GetExperiment: %v", err)
	}
	for _, needle := range []string{"Queue started", "GPU wait loop started.", "expected_rows", "Status changed to completed", "All rows are present."} {
		if !strings.Contains(got.Content, needle) {
			t.Fatalf("expected content to contain %q:\n%s", needle, got.Content)
		}
	}
}
