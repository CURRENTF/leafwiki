package research

import (
	"bytes"
	"encoding/json"
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

type researchErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type listExperimentsResponse struct {
	Experiments []coreresearch.Experiment `json:"experiments"`
}

type readDocumentResponse struct {
	Document coreresearch.Document `json:"document"`
}

func researchRequest(t *testing.T, method, rawURL, body string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, rawURL, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func authedResearchRequest(t *testing.T, server *httptest.Server, method, endpoint, body string) *http.Request {
	t.Helper()
	req := researchRequest(t, method, server.URL+endpoint, body)
	req.Header.Set("X-Research-Password", "secret-password")
	return req
}

func doResearchJSON(t *testing.T, req *http.Request, out interface{}) (int, string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.String(), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if out != nil {
		if len(strings.TrimSpace(string(body))) == 0 {
			t.Fatalf("empty JSON response for %s %s", req.Method, req.URL.String())
		}
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("decode JSON status=%d body=%s: %v", resp.StatusCode, string(body), err)
		}
	}
	return resp.StatusCode, string(body)
}

func createExperimentHTTP(t *testing.T, server *httptest.Server, body string, wantStatus int) coreresearch.Experiment {
	t.Helper()
	var exp coreresearch.Experiment
	status, respBody := doResearchJSON(t, authedResearchRequest(t, server, http.MethodPost, "/api/research/experiments", body), &exp)
	if status != wantStatus {
		t.Fatalf("create status = %d, want %d, body=%s", status, wantStatus, respBody)
	}
	return exp
}

func expectResearchError(t *testing.T, server *httptest.Server, method, endpoint, body string, wantStatus int, wantCode string) {
	t.Helper()
	var out researchErrorEnvelope
	status, respBody := doResearchJSON(t, authedResearchRequest(t, server, method, endpoint, body), &out)
	if status != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body=%s", method, endpoint, status, wantStatus, respBody)
	}
	if out.Error.Code != wantCode {
		t.Fatalf("%s %s error code = %q, want %q, body=%s", method, endpoint, out.Error.Code, wantCode, respBody)
	}
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

func TestResearchRoutesProtectAgentReadAPIs(t *testing.T) {
	server := newResearchTestRouter(t)
	defer server.Close()

	endpoints := []string{
		"/api/research/docs/search?q=needle",
		"/api/research/docs/read?path=projects/deltakv",
		"/api/research/docs/recent",
		"/api/research/experiments/missing/context",
	}
	for _, endpoint := range endpoints {
		req := researchRequest(t, http.MethodGet, server.URL+endpoint, "")
		status, body := doResearchJSON(t, req, nil)
		if status != http.StatusUnauthorized {
			t.Fatalf("GET %s status = %d, want %d, body=%s", endpoint, status, http.StatusUnauthorized, body)
		}
	}
}

func TestResearchRoutesReturnStructuredErrorsForBadInputs(t *testing.T) {
	server := newResearchTestRouter(t)
	defer server.Close()

	expectResearchError(t, server, http.MethodGet, "/api/research/docs/search", "", http.StatusBadRequest, "invalid_research_input")
	expectResearchError(t, server, http.MethodGet, "/api/research/docs/read", "", http.StatusBadRequest, "invalid_research_input")
	expectResearchError(t, server, http.MethodGet, "/api/research/docs/read?path=projects/deltakv/missing", "", http.StatusNotFound, "document_not_found")
	expectResearchError(t, server, http.MethodGet, "/api/research/experiments/missing", "", http.StatusNotFound, "experiment_not_found")
	expectResearchError(t, server, http.MethodPatch, "/api/research/experiments/missing/status", `{}`, http.StatusBadRequest, "invalid_research_input")
	expectResearchError(t, server, http.MethodPost, "/api/research/experiments/missing/events", `{"title":"Queue started"}`, http.StatusNotFound, "experiment_not_found")
}

func TestResearchRoutesExerciseExperimentLifecycleOverHTTP(t *testing.T) {
	server := newResearchTestRouter(t)
	defer server.Close()

	exp := createExperimentHTTP(t, server, `{
		"project":"DeltaKV",
		"title":"HTTP lifecycle run",
		"slugHint":"http-lifecycle-run",
		"status":"queued",
		"goal":"Track a full API lifecycle.",
		"command":"go test ./internal/research ./internal/wiki/research",
		"tags":["api","lifecycle"],
		"fingerprint":{"run_root":"/tmp/leafwiki-lifecycle"}
	}`, http.StatusCreated)
	if exp.ID != "deltakv-20260624-http-lifecycle-run" {
		t.Fatalf("id = %q, want deltakv-20260624-http-lifecycle-run", exp.ID)
	}
	if !exp.Created {
		t.Fatalf("created = false, want true")
	}
	if exp.CommitHash != "" {
		t.Fatalf("commitHash = %q, want empty without test committer", exp.CommitHash)
	}
	if exp.Status != "queued" {
		t.Fatalf("status = %q, want queued", exp.Status)
	}
	if !strings.Contains(exp.Content, "Track a full API lifecycle.") {
		t.Fatalf("created content missing goal:\n%s", exp.Content)
	}

	var eventOut coreresearch.Experiment
	status, body := doResearchJSON(t, authedResearchRequest(t, server, http.MethodPost, "/api/research/experiments/"+exp.ID+"/events", `{
		"title":"Started benchmark",
		"type":"run",
		"status":"running",
		"content":"The first shard reached the evaluator.",
		"metrics":{"validation_rows":128},
		"artifacts":[{"label":"stdout","path":"/tmp/leafwiki-lifecycle/stdout.log"}]
	}`), &eventOut)
	if status != http.StatusOK {
		t.Fatalf("append event status = %d, want %d, body=%s", status, http.StatusOK, body)
	}
	if eventOut.Status != "running" {
		t.Fatalf("event status = %q, want running", eventOut.Status)
	}
	for _, needle := range []string{"Started benchmark", "The first shard reached the evaluator.", "validation_rows", "stdout.log"} {
		if !strings.Contains(eventOut.Content, needle) {
			t.Fatalf("event content missing %q:\n%s", needle, eventOut.Content)
		}
	}

	var statusOut coreresearch.Experiment
	status, body = doResearchJSON(t, authedResearchRequest(t, server, http.MethodPatch, "/api/research/experiments/"+exp.ID+"/status", `{
		"status":"completed",
		"note":"All shards finished successfully."
	}`), &statusOut)
	if status != http.StatusOK {
		t.Fatalf("update status = %d, want %d, body=%s", status, http.StatusOK, body)
	}
	if statusOut.Status != "completed" {
		t.Fatalf("updated status = %q, want completed", statusOut.Status)
	}
	if !strings.Contains(statusOut.Content, "All shards finished successfully.") {
		t.Fatalf("status content missing note:\n%s", statusOut.Content)
	}

	var resultsOut coreresearch.Experiment
	status, body = doResearchJSON(t, authedResearchRequest(t, server, http.MethodPost, "/api/research/experiments/"+exp.ID+"/results", `{
		"status":"completed",
		"content":"Accuracy reached 0.91 with stable memory.",
		"metrics":{"accuracy":0.91},
		"artifacts":[{"label":"summary","url":"https://example.invalid/summary"}]
	}`), &resultsOut)
	if status != http.StatusOK {
		t.Fatalf("record results status = %d, want %d, body=%s", status, http.StatusOK, body)
	}
	for _, needle := range []string{"Results recorded", "Accuracy reached 0.91", "accuracy", "https://example.invalid/summary"} {
		if !strings.Contains(resultsOut.Content, needle) {
			t.Fatalf("results content missing %q:\n%s", needle, resultsOut.Content)
		}
	}

	var got coreresearch.Experiment
	status, body = doResearchJSON(t, authedResearchRequest(t, server, http.MethodGet, "/api/research/experiments/"+exp.ID, ""), &got)
	if status != http.StatusOK {
		t.Fatalf("get experiment status = %d, want %d, body=%s", status, http.StatusOK, body)
	}
	if got.ID != exp.ID || got.Status != "completed" {
		t.Fatalf("got experiment id/status = %q/%q, want %q/completed", got.ID, got.Status, exp.ID)
	}
	for _, needle := range []string{"Started benchmark", "All shards finished successfully.", "Accuracy reached 0.91"} {
		if !strings.Contains(got.Content, needle) {
			t.Fatalf("get experiment content missing %q:\n%s", needle, got.Content)
		}
	}
}

func TestResearchRoutesDeduplicateAndSuffixExperimentIDsOverHTTP(t *testing.T) {
	server := newResearchTestRouter(t)
	defer server.Close()

	first := createExperimentHTTP(t, server, `{
		"project":"DeltaKV",
		"title":"Natural language experiment id",
		"slugHint":"natural-language-id",
		"fingerprint":{"run_root":"/tmp/run-a"}
	}`, http.StatusCreated)
	second := createExperimentHTTP(t, server, `{
		"project":"DeltaKV",
		"title":"Natural language experiment id",
		"slugHint":"natural-language-id",
		"fingerprint":{"run_root":"/tmp/run-a"}
	}`, http.StatusOK)
	if second.ID != first.ID {
		t.Fatalf("same fingerprint id = %q, want %q", second.ID, first.ID)
	}
	if second.Created {
		t.Fatalf("same fingerprint should return created=false")
	}

	third := createExperimentHTTP(t, server, `{
		"project":"DeltaKV",
		"title":"Natural language experiment id",
		"slugHint":"natural-language-id",
		"fingerprint":{"run_root":"/tmp/run-b"}
	}`, http.StatusCreated)
	if third.ID != first.ID+"-02" {
		t.Fatalf("different fingerprint id = %q, want %q", third.ID, first.ID+"-02")
	}
	wantPath := "projects/deltakv/experiments/2026/06/" + third.ID
	if third.Path != wantPath {
		t.Fatalf("suffixed path = %q, want %q", third.Path, wantPath)
	}
}

func TestResearchRoutesListExperimentsFiltersProjectAndStatus(t *testing.T) {
	server := newResearchTestRouter(t)
	defer server.Close()

	createExperimentHTTP(t, server, `{"project":"DeltaKV","title":"List running","slugHint":"list-running","status":"running"}`, http.StatusCreated)
	completed := createExperimentHTTP(t, server, `{"project":"DeltaKV","title":"List completed","slugHint":"list-completed","status":"completed"}`, http.StatusCreated)
	createExperimentHTTP(t, server, `{"project":"OtherProject","title":"Other completed","slugHint":"other-completed","status":"completed"}`, http.StatusCreated)

	var out listExperimentsResponse
	status, body := doResearchJSON(t, authedResearchRequest(t, server, http.MethodGet, "/api/research/experiments?project=DeltaKV&status=completed", ""), &out)
	if status != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body=%s", status, http.StatusOK, body)
	}
	if len(out.Experiments) != 1 {
		t.Fatalf("list returned %d experiments, want 1: %#v", len(out.Experiments), out.Experiments)
	}
	if out.Experiments[0].ID != completed.ID || out.Experiments[0].Project != "deltakv" || out.Experiments[0].Status != "completed" {
		t.Fatalf("unexpected filtered experiment: %#v", out.Experiments[0])
	}
}

func TestResearchRoutesProvideSearchReadRecentAndContextForAgents(t *testing.T) {
	server := newResearchTestRouter(t)
	defer server.Close()

	target := createExperimentHTTP(t, server, `{
		"project":"DeltaKV",
		"title":"Context target",
		"slugHint":"context-target",
		"status":"running",
		"goal":"Main experiment whose context should exclude itself.",
		"tags":["context"]
	}`, http.StatusCreated)
	support := createExperimentHTTP(t, server, `{
		"project":"DeltaKV",
		"title":"Context support",
		"slugHint":"context-support",
		"status":"completed",
		"goal":"Support note with rare-context-token for related lookup."
	}`, http.StatusCreated)
	createExperimentHTTP(t, server, `{
		"project":"OtherProject",
		"title":"Other context",
		"slugHint":"other-context",
		"goal":"This document should not appear in DeltaKV filters."
	}`, http.StatusCreated)

	var searchOut coreresearch.SearchDocumentsOutput
	status, body := doResearchJSON(t, authedResearchRequest(t, server, http.MethodGet, "/api/research/docs/search?q=rare-context-token&project=DeltaKV&kind=page&limit=5", ""), &searchOut)
	if status != http.StatusOK {
		t.Fatalf("search status = %d, want %d, body=%s", status, http.StatusOK, body)
	}
	if searchOut.Count != 1 || len(searchOut.Items) != 1 || searchOut.Items[0].ID != support.PageID {
		t.Fatalf("search result = count %d items %#v, want support page %q", searchOut.Count, searchOut.Items, support.PageID)
	}

	var readOut readDocumentResponse
	status, body = doResearchJSON(t, authedResearchRequest(t, server, http.MethodGet, "/api/research/docs/read?id="+support.PageID, ""), &readOut)
	if status != http.StatusOK {
		t.Fatalf("read by id status = %d, want %d, body=%s", status, http.StatusOK, body)
	}
	if readOut.Document.ResearchID != support.ID || !strings.Contains(readOut.Document.Markdown, "rare-context-token") {
		t.Fatalf("read by id returned unexpected document: %#v", readOut.Document)
	}

	var recentOut coreresearch.RecentDocumentsOutput
	status, body = doResearchJSON(t, authedResearchRequest(t, server, http.MethodGet, "/api/research/docs/recent?project=DeltaKV&kind=page&limit=1", ""), &recentOut)
	if status != http.StatusOK {
		t.Fatalf("recent status = %d, want %d, body=%s", status, http.StatusOK, body)
	}
	if len(recentOut.Items) != 1 {
		t.Fatalf("recent returned %d items, want 1: %#v", len(recentOut.Items), recentOut.Items)
	}
	if recentOut.Items[0].Kind != "page" || recentOut.Items[0].Project != "deltakv" {
		t.Fatalf("recent item should be a DeltaKV page: %#v", recentOut.Items[0])
	}

	var contextOut coreresearch.ExperimentContext
	status, body = doResearchJSON(t, authedResearchRequest(t, server, http.MethodGet, "/api/research/experiments/"+target.ID+"/context?q=rare-context-token&limit=5", ""), &contextOut)
	if status != http.StatusOK {
		t.Fatalf("context status = %d, want %d, body=%s", status, http.StatusOK, body)
	}
	if contextOut.Experiment == nil || contextOut.Experiment.ID != target.ID {
		t.Fatalf("context experiment = %#v, want %q", contextOut.Experiment, target.ID)
	}
	if contextOut.Query != "rare-context-token" {
		t.Fatalf("context query = %q, want rare-context-token", contextOut.Query)
	}
	if !documentsContainID(contextOut.RelatedDocs, support.PageID) {
		t.Fatalf("related docs missing support page %q: %#v", support.PageID, contextOut.RelatedDocs)
	}
	if documentsContainID(contextOut.RelatedDocs, target.PageID) {
		t.Fatalf("related docs should exclude target page %q: %#v", target.PageID, contextOut.RelatedDocs)
	}
	if !documentsContainID(contextOut.RecentDocs, support.PageID) {
		t.Fatalf("recent docs missing support page %q: %#v", support.PageID, contextOut.RecentDocs)
	}
	for _, doc := range contextOut.RecentDocs {
		if doc.Kind != "page" {
			t.Fatalf("context recent docs should only contain pages: %#v", contextOut.RecentDocs)
		}
		if doc.ID == target.PageID {
			t.Fatalf("context recent docs should exclude target page: %#v", contextOut.RecentDocs)
		}
	}
}

func documentsContainID(docs []coreresearch.Document, id string) bool {
	for _, doc := range docs {
		if doc.ID == id {
			return true
		}
	}
	return false
}
