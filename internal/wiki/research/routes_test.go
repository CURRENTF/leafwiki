package research

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/perber/wiki/internal/core/tree"
	httpinternal "github.com/perber/wiki/internal/http"
	coreresearch "github.com/perber/wiki/internal/research"
	coresearch "github.com/perber/wiki/internal/search"
)

func newResearchTestRouter(t *testing.T) *httptest.Server {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "schema.json"), []byte(fmt.Sprintf(`{"version":%d}`, tree.CurrentSchemaVersion)), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	treeSvc := tree.NewTreeService(tmp)
	if err := treeSvc.LoadTree(); err != nil {
		t.Fatalf("load tree: %v", err)
	}
	index, err := coresearch.NewSQLiteIndex(tmp)
	if err != nil {
		t.Fatalf("new search index: %v", err)
	}
	t.Cleanup(func() {
		if err := index.Close(); err != nil {
			t.Fatalf("close search index: %v", err)
		}
	})
	svc := coreresearch.NewService(coreresearch.Config{
		Tree:   treeSvc,
		Search: index,
		Now: func() time.Time {
			return time.Date(2026, 6, 24, 4, 5, 6, 0, time.UTC)
		},
	})
	routes := NewRoutes(RoutesConfig{
		Service:     svc,
		APIToken:    "secret-token",
		APIPassword: "secret-password",
	})
	router := httpinternal.NewRouter(
		[]httpinternal.RouteRegistrar{routes},
		httpinternal.FrontendConfig{StorageDir: tmp},
		httpinternal.RouterOptions{AllowInsecure: true},
	)
	return httptest.NewServer(router)
}

func TestResearchRoutesRequireBearerTokenForAgentWrites(t *testing.T) {
	server := newResearchTestRouter(t)
	defer server.Close()

	body := `{"project":"DeltaKV","title":"Qwen3 KVzip","slugHint":"qwen3-kvzip"}`
	resp, err := http.Post(server.URL+"/api/research/experiments", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post without token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/research/experiments", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post with token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status with token = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
}

func TestResearchRoutesAcceptResearchPassword(t *testing.T) {
	server := newResearchTestRouter(t)
	defer server.Close()

	body := `{"project":"DeltaKV","title":"Qwen3 KVzip","slugHint":"qwen3-kvzip"}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/research/experiments", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Research-Password", "secret-password")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post with research password: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status with password = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
}

func TestResearchRoutesAcceptBasicAuthPassword(t *testing.T) {
	server := newResearchTestRouter(t)
	defer server.Close()

	body := `{"project":"DeltaKV","title":"Qwen3 KVzip","slugHint":"qwen3-kvzip"}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/research/experiments", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("research-agent", "secret-password")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post with basic auth: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status with basic auth = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
}

func TestResearchRoutesExposeDocumentSearchAndReadForAgents(t *testing.T) {
	server := newResearchTestRouter(t)
	defer server.Close()

	body := `{"project":"DeltaKV","title":"Agent readable run","slugHint":"agent-readable-run","goal":"Preserve a unique context-token for agent lookup."}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/research/experiments", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Research-Password", "secret-password")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	req, err = http.NewRequest(http.MethodGet, server.URL+"/api/research/docs/search?q=context-token&project=DeltaKV&kind=page", nil)
	if err != nil {
		t.Fatalf("new search request: %v", err)
	}
	req.Header.Set("X-Research-Password", "secret-password")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("search documents: %v", err)
	}
	defer resp.Body.Close()
	searchBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read search body: %v", err)
	}
	searchBody := string(searchBytes)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search status = %d, body=%s", resp.StatusCode, searchBody)
	}
	if !strings.Contains(searchBody, `"path":"projects/deltakv/experiments/2026/06/deltakv-20260624-agent-readable-run"`) {
		t.Fatalf("search body missing experiment path:\n%s", searchBody)
	}

	req, err = http.NewRequest(http.MethodGet, server.URL+"/api/research/docs/read?path=projects/deltakv/experiments/2026/06/deltakv-20260624-agent-readable-run", nil)
	if err != nil {
		t.Fatalf("new read request: %v", err)
	}
	req.Header.Set("X-Research-Password", "secret-password")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	defer resp.Body.Close()
	readBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read document body: %v", err)
	}
	readBody := string(readBytes)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read status = %d, body=%s", resp.StatusCode, readBody)
	}
	if !strings.Contains(readBody, `Preserve a unique context-token for agent lookup.`) {
		t.Fatalf("read body missing markdown:\n%s", readBody)
	}
}
