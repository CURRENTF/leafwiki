package research

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/perber/wiki/internal/core/excerpt"
	"github.com/perber/wiki/internal/core/markdown"
	"github.com/perber/wiki/internal/core/tree"
	coresearch "github.com/perber/wiki/internal/search"
)

const (
	DefaultAgentUserID = "research-agent"

	FieldID          = "research_id"
	FieldKind        = "research_kind"
	FieldProject     = "research_project"
	FieldStatus      = "research_status"
	FieldSlugHint    = "research_slug_hint"
	FieldCreatedAt   = "research_created_at"
	FieldUpdatedAt   = "research_updated_at"
	FieldTitle       = "research_title"
	FieldTags        = "research_tags"
	FieldFingerprint = "research_fingerprint"
)

var (
	ErrInvalidInput       = errors.New("invalid research input")
	ErrExperimentNotFound = errors.New("research experiment not found")
	ErrDocumentNotFound   = errors.New("research document not found")
	ErrSearchUnavailable  = errors.New("research document search is unavailable")
)

type Service struct {
	tree      *tree.TreeService
	slugger   *tree.SlugService
	search    *coresearch.SQLiteIndex
	committer *GitCommitter
	now       func() time.Time
	mu        sync.Mutex
}

type Config struct {
	Tree      *tree.TreeService
	Search    *coresearch.SQLiteIndex
	Committer *GitCommitter
	Now       func() time.Time
}

func NewService(cfg Config) *Service {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		tree:      cfg.Tree,
		slugger:   tree.NewSlugService(),
		search:    cfg.Search,
		committer: cfg.Committer,
		now:       now,
	}
}

type CreateExperimentInput struct {
	UserID      string                 `json:"-"`
	Project     string                 `json:"project"`
	Title       string                 `json:"title"`
	SlugHint    string                 `json:"slugHint"`
	Status      string                 `json:"status"`
	Goal        string                 `json:"goal"`
	Command     string                 `json:"command"`
	WorkingDir  string                 `json:"workingDir"`
	Repo        string                 `json:"repo"`
	GitCommit   string                 `json:"gitCommit"`
	Host        string                 `json:"host"`
	Model       string                 `json:"model"`
	Method      string                 `json:"method"`
	Benchmark   string                 `json:"benchmark"`
	Tags        []string               `json:"tags"`
	Fingerprint map[string]interface{} `json:"fingerprint"`
	Metadata    map[string]interface{} `json:"metadata"`
	Content     string                 `json:"content"`
}

type AppendEventInput struct {
	UserID    string                 `json:"-"`
	ID        string                 `json:"-"`
	Title     string                 `json:"title"`
	Type      string                 `json:"type"`
	Status    string                 `json:"status"`
	Content   string                 `json:"content"`
	Metrics   map[string]interface{} `json:"metrics"`
	Artifacts []Artifact             `json:"artifacts"`
}

type UpdateStatusInput struct {
	UserID string `json:"-"`
	ID     string `json:"-"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

type RecordResultsInput struct {
	UserID    string                 `json:"-"`
	ID        string                 `json:"-"`
	Status    string                 `json:"status"`
	Content   string                 `json:"content"`
	Metrics   map[string]interface{} `json:"metrics"`
	Artifacts []Artifact             `json:"artifacts"`
}

type Artifact struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	URL   string `json:"url"`
}

type Experiment struct {
	ID          string                 `json:"id"`
	PageID      string                 `json:"pageId"`
	Path        string                 `json:"path"`
	Title       string                 `json:"title"`
	Project     string                 `json:"project"`
	Status      string                 `json:"status"`
	Tags        []string               `json:"tags,omitempty"`
	Fingerprint map[string]interface{} `json:"fingerprint,omitempty"`
	Content     string                 `json:"content,omitempty"`
	Created     bool                   `json:"created,omitempty"`
	CommitHash  string                 `json:"commitHash,omitempty"`
}

type Document struct {
	ID           string                 `json:"id"`
	Path         string                 `json:"path"`
	Title        string                 `json:"title"`
	Kind         string                 `json:"kind"`
	Project      string                 `json:"project,omitempty"`
	ResearchID   string                 `json:"researchId,omitempty"`
	ResearchKind string                 `json:"researchKind,omitempty"`
	Status       string                 `json:"status,omitempty"`
	Tags         []string               `json:"tags,omitempty"`
	Snippet      string                 `json:"snippet,omitempty"`
	Markdown     string                 `json:"markdown,omitempty"`
	Frontmatter  map[string]interface{} `json:"frontmatter,omitempty"`
	CreatedAt    string                 `json:"createdAt,omitempty"`
	UpdatedAt    string                 `json:"updatedAt,omitempty"`
}

type SearchDocumentsInput struct {
	Query   string
	Project string
	Kind    string
	Offset  int
	Limit   int
}

type SearchDocumentsOutput struct {
	Query  string     `json:"query"`
	Count  int        `json:"count"`
	Offset int        `json:"offset"`
	Limit  int        `json:"limit"`
	Items  []Document `json:"items"`
}

type ReadDocumentInput struct {
	ID   string
	Path string
}

type RecentDocumentsInput struct {
	Project string
	Kind    string
	Limit   int
}

type RecentDocumentsOutput struct {
	Items []Document `json:"items"`
}

type ExperimentContext struct {
	Experiment  *Experiment `json:"experiment"`
	Query       string      `json:"query"`
	RelatedDocs []Document  `json:"relatedDocs"`
	RecentDocs  []Document  `json:"recentDocs"`
}

func (s *Service) CreateExperiment(ctx context.Context, input CreateExperimentInput) (*Experiment, error) {
	_ = ctx
	if s == nil || s.tree == nil {
		return nil, fmt.Errorf("research service is not initialized")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	userID := normalizeUserID(input.UserID)
	project := s.slugger.GenerateValidSlug(input.Project)
	if project == "" {
		return nil, fmt.Errorf("%w: project is required", ErrInvalidInput)
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}

	slugSource := firstNonEmpty(input.SlugHint, input.Title)
	slug := s.slugger.GenerateValidSlug(slugSource)
	if slug == "" {
		return nil, fmt.Errorf("%w: slugHint or title must produce a valid slug", ErrInvalidInput)
	}

	now := s.now().UTC()
	day := now.Format("20060102")
	baseID := fmt.Sprintf("%s-%s-%s", project, day, slug)
	parentPath := fmt.Sprintf("projects/%s/experiments/%s/%s", project, now.Format("2006"), now.Format("01"))
	parentID, err := s.ensureSectionPath(userID, parentPath)
	if err != nil {
		return nil, err
	}

	var canonicalID string
	var existing *Experiment
	for i := 0; ; i++ {
		candidate := withCollisionSuffix(baseID, i)
		page, err := s.tree.FindPageByRoutePath(parentPath + "/" + candidate)
		if err != nil {
			if errors.Is(err, tree.ErrPageNotFound) {
				canonicalID = candidate
				break
			}
			return nil, err
		}
		exp, err := s.experimentFromPage(page, true)
		if err != nil {
			return nil, err
		}
		if sameFingerprint(exp.Fingerprint, input.Fingerprint) && len(input.Fingerprint) > 0 {
			existing = exp
			break
		}
	}
	if existing != nil {
		return existing, nil
	}

	kind := tree.NodeKindPage
	pageID, err := s.tree.CreateNode(userID, &parentID, title, canonicalID, &kind)
	if err != nil {
		return nil, err
	}
	page, err := s.tree.GetPage(*pageID)
	if err != nil {
		return nil, err
	}

	status := normalizeStatus(input.Status, "draft")
	fm := markdown.Frontmatter{
		ExtraFields: s.createExperimentFields(canonicalID, project, slug, title, status, now, input),
	}
	body := buildExperimentBody(title, project, status, input)
	raw, err := markdown.BuildMarkdownWithFrontmatter(fm, body)
	if err != nil {
		return nil, err
	}
	if err := s.tree.UpdateNode(userID, page.ID, title, canonicalID, &raw, page.Version(), true); err != nil {
		return nil, err
	}
	if err := s.indexPageByID(page.ID); err != nil {
		return nil, err
	}

	commitHash, err := s.commit(fmt.Sprintf("research: create %s", canonicalID))
	if err != nil {
		return nil, err
	}
	created, err := s.tree.GetPage(page.ID)
	if err != nil {
		return nil, err
	}
	out, err := s.experimentFromPage(created, true)
	if err != nil {
		return nil, err
	}
	out.Created = true
	out.CommitHash = commitHash
	return out, nil
}

func (s *Service) AppendEvent(ctx context.Context, input AppendEventInput) (*Experiment, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	page, fm, body, err := s.loadExperiment(input.ID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if input.Status != "" {
		fm.ExtraFields[FieldStatus] = normalizeStatus(input.Status, "")
	}
	fm.ExtraFields[FieldUpdatedAt] = now.Format(time.RFC3339)
	body = appendSection(body, buildEventSection("Event", now, input.Title, input.Type, input.Status, input.Content, input.Metrics, input.Artifacts))
	if err := s.writeExperiment(page, fm, body, normalizeUserID(input.UserID)); err != nil {
		return nil, err
	}
	commitHash, err := s.commit(fmt.Sprintf("research: append event to %s", input.ID))
	if err != nil {
		return nil, err
	}
	updated, err := s.tree.GetPage(page.ID)
	if err != nil {
		return nil, err
	}
	out, err := s.experimentFromPage(updated, true)
	if err != nil {
		return nil, err
	}
	out.CommitHash = commitHash
	return out, nil
}

func (s *Service) UpdateStatus(ctx context.Context, input UpdateStatusInput) (*Experiment, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	status := normalizeStatus(input.Status, "")
	if status == "" {
		return nil, fmt.Errorf("%w: status is required", ErrInvalidInput)
	}
	page, fm, body, err := s.loadExperiment(input.ID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	fm.ExtraFields[FieldStatus] = status
	fm.ExtraFields[FieldUpdatedAt] = now.Format(time.RFC3339)
	if strings.TrimSpace(input.Note) != "" {
		body = appendSection(body, buildEventSection("Status", now, "Status changed to "+status, "", status, input.Note, nil, nil))
	}
	if err := s.writeExperiment(page, fm, body, normalizeUserID(input.UserID)); err != nil {
		return nil, err
	}
	commitHash, err := s.commit(fmt.Sprintf("research: update status for %s", input.ID))
	if err != nil {
		return nil, err
	}
	updated, err := s.tree.GetPage(page.ID)
	if err != nil {
		return nil, err
	}
	out, err := s.experimentFromPage(updated, true)
	if err != nil {
		return nil, err
	}
	out.CommitHash = commitHash
	return out, nil
}

func (s *Service) RecordResults(ctx context.Context, input RecordResultsInput) (*Experiment, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	page, fm, body, err := s.loadExperiment(input.ID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if input.Status != "" {
		fm.ExtraFields[FieldStatus] = normalizeStatus(input.Status, "")
	}
	fm.ExtraFields[FieldUpdatedAt] = now.Format(time.RFC3339)
	fm.ExtraFields["research_results_recorded_at"] = now.Format(time.RFC3339)
	body = appendSection(body, buildEventSection("Results", now, "Results recorded", "results", input.Status, input.Content, input.Metrics, input.Artifacts))
	if err := s.writeExperiment(page, fm, body, normalizeUserID(input.UserID)); err != nil {
		return nil, err
	}
	commitHash, err := s.commit(fmt.Sprintf("research: record results for %s", input.ID))
	if err != nil {
		return nil, err
	}
	updated, err := s.tree.GetPage(page.ID)
	if err != nil {
		return nil, err
	}
	out, err := s.experimentFromPage(updated, true)
	if err != nil {
		return nil, err
	}
	out.CommitHash = commitHash
	return out, nil
}

func (s *Service) GetExperiment(ctx context.Context, id string) (*Experiment, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	page, _, _, err := s.loadExperiment(id)
	if err != nil {
		return nil, err
	}
	return s.experimentFromPage(page, true)
}

func (s *Service) ListExperiments(ctx context.Context, projectFilter, statusFilter string) ([]Experiment, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	projectFilter = s.slugger.GenerateValidSlug(projectFilter)
	statusFilter = normalizeStatus(statusFilter, "")
	var out []Experiment
	if err := s.tree.WalkNodes(func(id string) error {
		page, err := s.tree.GetPage(id)
		if err != nil {
			return nil
		}
		exp, err := s.experimentFromPage(page, false)
		if err != nil || exp.ID == "" {
			return nil
		}
		if projectFilter != "" && exp.Project != projectFilter {
			return nil
		}
		if statusFilter != "" && exp.Status != statusFilter {
			return nil
		}
		out = append(out, *exp)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (s *Service) SearchDocuments(ctx context.Context, input SearchDocumentsInput) (*SearchDocumentsOutput, error) {
	_ = ctx
	if s == nil || s.tree == nil {
		return nil, fmt.Errorf("research service is not initialized")
	}
	if s.search == nil {
		return nil, ErrSearchUnavailable
	}

	query := strings.TrimSpace(input.Query)
	if query == "" {
		return nil, fmt.Errorf("%w: q is required", ErrInvalidInput)
	}
	offset := normalizeOffset(input.Offset)
	limit := normalizeLimit(input.Limit, 20, 50)
	project := s.slugger.GenerateValidSlug(input.Project)
	kind := normalizeNodeKind(input.Kind)

	s.mu.Lock()
	defer s.mu.Unlock()

	pageIDs, err := s.filteredPageIDs(project, kind)
	if err != nil {
		return nil, err
	}
	result, err := s.search.Search(query, pageIDs, offset, limit)
	if err != nil {
		return nil, err
	}
	out := &SearchDocumentsOutput{
		Query:  query,
		Count:  result.Count,
		Offset: result.Offset,
		Limit:  result.Limit,
		Items:  make([]Document, 0, len(result.Items)),
	}
	for _, item := range result.Items {
		page, err := s.tree.GetPage(item.PageID)
		if err != nil {
			continue
		}
		doc, err := s.documentFromPage(page, false)
		if err != nil {
			continue
		}
		doc.Snippet = plainSnippet(item.Excerpt)
		out.Items = append(out.Items, *doc)
	}
	return out, nil
}

func (s *Service) ReadDocument(ctx context.Context, input ReadDocumentInput) (*Document, error) {
	_ = ctx
	if s == nil || s.tree == nil {
		return nil, fmt.Errorf("research service is not initialized")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	page, err := s.findDocument(input.ID, input.Path)
	if err != nil {
		return nil, err
	}
	return s.documentFromPage(page, true)
}

func (s *Service) RecentDocuments(ctx context.Context, input RecentDocumentsInput) (*RecentDocumentsOutput, error) {
	_ = ctx
	if s == nil || s.tree == nil {
		return nil, fmt.Errorf("research service is not initialized")
	}

	limit := normalizeLimit(input.Limit, 20, 50)
	project := s.slugger.GenerateValidSlug(input.Project)
	kind := normalizeNodeKind(input.Kind)

	s.mu.Lock()
	defer s.mu.Unlock()

	docs := make([]Document, 0)
	if err := s.tree.WalkNodes(func(id string) error {
		page, err := s.tree.GetPage(id)
		if err != nil {
			return nil
		}
		if !matchesDocumentFilter(page, project, kind) {
			return nil
		}
		doc, err := s.documentFromPage(page, false)
		if err != nil {
			return nil
		}
		docs = append(docs, *doc)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.SliceStable(docs, func(i, j int) bool {
		left := docs[i].UpdatedAt
		right := docs[j].UpdatedAt
		if left == right {
			return docs[i].Path < docs[j].Path
		}
		return left > right
	})
	if len(docs) > limit {
		docs = docs[:limit]
	}
	return &RecentDocumentsOutput{Items: docs}, nil
}

func (s *Service) GetExperimentContext(ctx context.Context, id, query string, limit int) (*ExperimentContext, error) {
	_ = ctx
	s.mu.Lock()
	page, _, _, err := s.loadExperiment(id)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	exp, err := s.experimentFromPage(page, true)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	limit = normalizeLimit(limit, 10, 30)
	query = strings.TrimSpace(query)
	if query == "" {
		query = strings.TrimSpace(strings.Join(append([]string{exp.Title}, exp.Tags...), " "))
	}

	var related []Document
	if query != "" && s.search != nil {
		searchOut, err := s.SearchDocuments(ctx, SearchDocumentsInput{
			Query:   query,
			Project: exp.Project,
			Limit:   limit,
		})
		if err != nil && !errors.Is(err, ErrInvalidInput) {
			return nil, err
		}
		if searchOut != nil {
			related = excludeDocument(searchOut.Items, exp.PageID)
		}
	}

	recentOut, err := s.RecentDocuments(ctx, RecentDocumentsInput{
		Project: exp.Project,
		Kind:    string(tree.NodeKindPage),
		Limit:   limit,
	})
	if err != nil {
		return nil, err
	}
	recent := excludeDocument(recentOut.Items, exp.PageID)
	if len(recent) > limit {
		recent = recent[:limit]
	}

	return &ExperimentContext{
		Experiment:  exp,
		Query:       query,
		RelatedDocs: related,
		RecentDocs:  recent,
	}, nil
}

func (s *Service) createExperimentFields(id, project, slug, title, status string, now time.Time, input CreateExperimentInput) map[string]interface{} {
	fields := map[string]interface{}{
		FieldID:        id,
		FieldKind:      "experiment",
		FieldProject:   project,
		FieldStatus:    status,
		FieldSlugHint:  slug,
		FieldTitle:     title,
		FieldCreatedAt: now.Format(time.RFC3339),
		FieldUpdatedAt: now.Format(time.RFC3339),
	}
	if len(input.Tags) > 0 {
		fields[FieldTags] = normalizeStringSlice(input.Tags)
	}
	if len(input.Fingerprint) > 0 {
		fields[FieldFingerprint] = input.Fingerprint
	}
	copyStringField(fields, "research_model", input.Model)
	copyStringField(fields, "research_method", input.Method)
	copyStringField(fields, "research_benchmark", input.Benchmark)
	copyStringField(fields, "research_repo", input.Repo)
	copyStringField(fields, "research_git_commit", input.GitCommit)
	copyStringField(fields, "research_host", input.Host)
	copyStringField(fields, "research_working_dir", input.WorkingDir)
	for key, value := range input.Metadata {
		key = s.slugger.GenerateValidSlug(key)
		if key == "" {
			continue
		}
		fields["research_meta_"+strings.ReplaceAll(key, "-", "_")] = value
	}
	return fields
}

func (s *Service) ensureSectionPath(userID, routePath string) (string, error) {
	segments := strings.Split(strings.Trim(routePath, "/"), "/")
	parentID := "root"
	soFar := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		soFar = append(soFar, segment)
		currentPath := strings.Join(soFar, "/")
		page, err := s.tree.FindPageByRoutePath(currentPath)
		if err == nil {
			parentID = page.ID
			continue
		}
		if !errors.Is(err, tree.ErrPageNotFound) {
			return "", err
		}
		title := titleFromSlug(segment)
		kind := tree.NodeKindSection
		newID, err := s.tree.CreateNode(userID, &parentID, title, segment, &kind)
		if err != nil {
			return "", err
		}
		parentID = *newID
	}
	return parentID, nil
}

func (s *Service) loadExperiment(id string) (*tree.Page, markdown.Frontmatter, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, markdown.Frontmatter{}, "", fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	var found *tree.Page
	if err := s.tree.WalkNodes(func(pageID string) error {
		page, err := s.tree.GetPage(pageID)
		if err != nil {
			return nil
		}
		exp, err := s.experimentFromPage(page, false)
		if err != nil || exp.ID != id {
			return nil
		}
		found = page
		return ErrStopWalk
	}); err != nil && !errors.Is(err, ErrStopWalk) {
		return nil, markdown.Frontmatter{}, "", err
	}
	if found == nil {
		return nil, markdown.Frontmatter{}, "", ErrExperimentNotFound
	}

	raw, err := s.tree.ReadPageRaw(found.ID)
	if err != nil {
		return nil, markdown.Frontmatter{}, "", err
	}
	fm, body, has, err := markdown.ParseFrontmatter(raw)
	if err != nil {
		return nil, markdown.Frontmatter{}, "", err
	}
	if !has {
		fm = markdown.Frontmatter{ExtraFields: map[string]interface{}{}}
	}
	if fm.ExtraFields == nil {
		fm.ExtraFields = map[string]interface{}{}
	}
	return found, fm, body, nil
}

var ErrStopWalk = errors.New("stop research walk")

func (s *Service) writeExperiment(page *tree.Page, fm markdown.Frontmatter, body string, userID string) error {
	if fm.ExtraFields == nil {
		fm.ExtraFields = map[string]interface{}{}
	}
	raw, err := markdown.BuildMarkdownWithFrontmatter(fm, body)
	if err != nil {
		return err
	}
	if err := s.tree.UpdateNode(userID, page.ID, page.Title, page.Slug, &raw, page.Version(), true); err != nil {
		return err
	}
	return s.indexPageByID(page.ID)
}

func (s *Service) experimentFromPage(page *tree.Page, includeContent bool) (*Experiment, error) {
	if page == nil {
		return nil, ErrExperimentNotFound
	}
	raw, err := s.tree.ReadPageRaw(page.ID)
	if err != nil {
		return nil, err
	}
	fm, body, has, err := markdown.ParseFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	if !has || fm.ExtraFields == nil {
		return &Experiment{}, nil
	}
	id := stringField(fm.ExtraFields, FieldID)
	if id == "" || stringField(fm.ExtraFields, FieldKind) != "experiment" {
		return &Experiment{}, nil
	}
	exp := &Experiment{
		ID:          id,
		PageID:      page.ID,
		Path:        strings.Trim(page.CalculatePath(), "/"),
		Title:       firstNonEmpty(stringField(fm.ExtraFields, FieldTitle), page.Title),
		Project:     stringField(fm.ExtraFields, FieldProject),
		Status:      stringField(fm.ExtraFields, FieldStatus),
		Tags:        stringSliceField(fm.ExtraFields, FieldTags),
		Fingerprint: mapField(fm.ExtraFields, FieldFingerprint),
	}
	if includeContent {
		exp.Content = body
	}
	return exp, nil
}

func (s *Service) findDocument(id, path string) (*tree.Page, error) {
	id = strings.TrimSpace(id)
	path = normalizeRoutePath(path)
	if id == "" && path == "" {
		return nil, fmt.Errorf("%w: id or path is required", ErrInvalidInput)
	}
	if id != "" {
		page, err := s.tree.GetPage(id)
		if err != nil {
			if errors.Is(err, tree.ErrPageNotFound) {
				return nil, ErrDocumentNotFound
			}
			return nil, err
		}
		return page, nil
	}
	page, err := s.tree.FindPageByRoutePath(path)
	if err != nil {
		if errors.Is(err, tree.ErrPageNotFound) {
			return nil, ErrDocumentNotFound
		}
		return nil, err
	}
	return page, nil
}

func (s *Service) documentFromPage(page *tree.Page, includeMarkdown bool) (*Document, error) {
	if page == nil {
		return nil, ErrDocumentNotFound
	}
	raw, err := s.tree.ReadPageRaw(page.ID)
	if err != nil {
		return nil, err
	}
	fm, body, has, err := markdown.ParseFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	if !has {
		fm = markdown.Frontmatter{ExtraFields: map[string]interface{}{}}
	}
	if fm.ExtraFields == nil {
		fm.ExtraFields = map[string]interface{}{}
	}

	path := strings.Trim(page.CalculatePath(), "/")
	doc := &Document{
		ID:           page.ID,
		Path:         path,
		Title:        page.Title,
		Kind:         string(page.Kind),
		Project:      firstNonEmpty(stringField(fm.ExtraFields, FieldProject), projectFromPath(path)),
		ResearchID:   stringField(fm.ExtraFields, FieldID),
		ResearchKind: stringField(fm.ExtraFields, FieldKind),
		Status:       stringField(fm.ExtraFields, FieldStatus),
		Tags:         stringSliceField(fm.ExtraFields, FieldTags),
		Snippet:      excerpt.FromBody(body),
		CreatedAt:    formatTime(page.Metadata.CreatedAt),
		UpdatedAt:    formatTime(page.Metadata.UpdatedAt),
	}
	if len(fm.ExtraFields) > 0 {
		doc.Frontmatter = fm.ExtraFields
	}
	if includeMarkdown {
		doc.Markdown = body
	}
	return doc, nil
}

func (s *Service) filteredPageIDs(project, kind string) ([]string, error) {
	if project == "" && kind == "" {
		return nil, nil
	}
	pageIDs := make([]string, 0)
	if err := s.tree.WalkNodes(func(id string) error {
		page, err := s.tree.GetPage(id)
		if err != nil {
			return nil
		}
		if matchesDocumentFilter(page, project, kind) {
			pageIDs = append(pageIDs, page.ID)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return pageIDs, nil
}

func matchesDocumentFilter(page *tree.Page, project, kind string) bool {
	if page == nil {
		return false
	}
	if kind != "" && string(page.Kind) != kind {
		return false
	}
	if project == "" {
		return true
	}
	return pathHasProject(strings.Trim(page.CalculatePath(), "/"), project)
}

func (s *Service) indexPageByID(pageID string) error {
	if s.search == nil {
		return nil
	}
	page, err := s.tree.GetPage(pageID)
	if err != nil {
		return err
	}
	path := strings.Trim(page.CalculatePath(), "/")
	filePath := path
	if filePath != "" {
		filePath += ".md"
	}
	return s.search.IndexPage(path, filePath, page.ID, page.Title, page.Kind, page.RawContent)
}

func (s *Service) commit(message string) (string, error) {
	if s.committer == nil {
		return "", nil
	}
	return s.committer.Commit(message)
}

func buildExperimentBody(title, project, status string, input CreateExperimentInput) string {
	if strings.TrimSpace(input.Content) != "" {
		return strings.TrimRight(input.Content, "\n") + "\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "## Status\n\n- Status: %s\n- Project: `%s`\n", status, project)
	writeBullet(&b, "Model", input.Model)
	writeBullet(&b, "Method", input.Method)
	writeBullet(&b, "Benchmark", input.Benchmark)
	writeBullet(&b, "Host", input.Host)
	writeBullet(&b, "Repo", input.Repo)
	writeBullet(&b, "Git commit", input.GitCommit)
	writeBullet(&b, "Working dir", input.WorkingDir)
	if len(input.Tags) > 0 {
		fmt.Fprintf(&b, "- Tags: `%s`\n", strings.Join(normalizeStringSlice(input.Tags), "`, `"))
	}
	b.WriteString("\n## Goal\n\n")
	if strings.TrimSpace(input.Goal) != "" {
		b.WriteString(strings.TrimSpace(input.Goal))
	} else {
		b.WriteString("_No goal recorded yet._")
	}
	b.WriteString("\n")
	if strings.TrimSpace(input.Command) != "" {
		b.WriteString("\n## Command\n\n```bash\n")
		b.WriteString(strings.TrimSpace(input.Command))
		b.WriteString("\n```\n")
	}
	b.WriteString("\n## Results\n\n_No results recorded yet._\n\n## Interpretation\n\n_No interpretation recorded yet._\n")
	return b.String()
}

func buildEventSection(sectionKind string, now time.Time, title, eventType, status, content string, metrics map[string]interface{}, artifacts []Artifact) string {
	title = firstNonEmpty(title, sectionKind)
	var b strings.Builder
	fmt.Fprintf(&b, "## %s - %s - %s\n\n", sectionKind, now.Format(time.RFC3339), title)
	writeBullet(&b, "Type", eventType)
	writeBullet(&b, "Status", status)
	if len(metrics) > 0 {
		b.WriteString("- Metrics:\n")
		for _, key := range sortedKeys(metrics) {
			fmt.Fprintf(&b, "  - `%s`: `%v`\n", key, metrics[key])
		}
	}
	if len(artifacts) > 0 {
		b.WriteString("- Artifacts:\n")
		for _, artifact := range artifacts {
			label := firstNonEmpty(artifact.Label, artifact.Path, artifact.URL)
			switch {
			case artifact.URL != "":
				fmt.Fprintf(&b, "  - [%s](%s)\n", label, artifact.URL)
			case artifact.Path != "":
				fmt.Fprintf(&b, "  - `%s`: `%s`\n", label, artifact.Path)
			default:
				fmt.Fprintf(&b, "  - `%s`\n", label)
			}
		}
	}
	if strings.TrimSpace(content) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(content))
		b.WriteString("\n")
	}
	return b.String()
}

func appendSection(body, section string) string {
	body = strings.TrimRight(body, "\n")
	section = strings.TrimRight(section, "\n")
	return body + "\n\n" + section + "\n"
}

func withCollisionSuffix(base string, index int) string {
	if index == 0 {
		return base
	}
	return fmt.Sprintf("%s-%02d", base, index+1)
}

func sameFingerprint(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for key, av := range a {
		if fmt.Sprint(av) != fmt.Sprint(b[key]) {
			return false
		}
	}
	return true
}

func normalizeUserID(userID string) string {
	if strings.TrimSpace(userID) == "" {
		return DefaultAgentUserID
	}
	return strings.TrimSpace(userID)
}

func normalizeStatus(value, fallback string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	if value == "" {
		return fallback
	}
	return value
}

func normalizeStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func titleFromSlug(slug string) string {
	parts := strings.Split(slug, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func writeBullet(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(b, "- %s: `%s`\n", label, strings.TrimSpace(value))
}

func copyStringField(fields map[string]interface{}, key string, value string) {
	if strings.TrimSpace(value) != "" {
		fields[key] = strings.TrimSpace(value)
	}
}

func stringField(fields map[string]interface{}, key string) string {
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func stringSliceField(fields map[string]interface{}, key string) []string {
	raw, ok := fields[key]
	if !ok || raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return normalizeStringSlice(typed)
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			out = append(out, fmt.Sprint(value))
		}
		return normalizeStringSlice(out)
	default:
		return normalizeStringSlice([]string{fmt.Sprint(typed)})
	}
}

func mapField(fields map[string]interface{}, key string) map[string]interface{} {
	raw, ok := fields[key]
	if !ok || raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case map[string]interface{}:
		return typed
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(typed))
		for k, v := range typed {
			out[fmt.Sprint(k)] = v
		}
		return out
	default:
		return nil
	}
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalizeRoutePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".md")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return ""
		}
		clean = append(clean, part)
	}
	return strings.Join(clean, "/")
}

func normalizeOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func normalizeLimit(limit, fallback, max int) int {
	if limit <= 0 {
		limit = fallback
	}
	if limit > max {
		return max
	}
	return limit
}

func normalizeNodeKind(kind string) string {
	kind = strings.TrimSpace(strings.ToLower(kind))
	switch kind {
	case "", string(tree.NodeKindPage), string(tree.NodeKindSection):
		return kind
	default:
		return ""
	}
}

func projectFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "projects" {
		return parts[1]
	}
	return ""
}

func pathHasProject(path, project string) bool {
	return strings.HasPrefix(strings.Trim(path, "/")+"/", "projects/"+project+"/")
}

func plainSnippet(snippet string) string {
	snippet = strings.ReplaceAll(snippet, "<b>", "")
	snippet = strings.ReplaceAll(snippet, "</b>", "")
	return strings.TrimSpace(snippet)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func excludeDocument(items []Document, pageID string) []Document {
	out := make([]Document, 0, len(items))
	for _, item := range items {
		if item.ID == pageID {
			continue
		}
		out = append(out, item)
	}
	return out
}
