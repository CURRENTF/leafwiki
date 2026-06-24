package research

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	httpinternal "github.com/perber/wiki/internal/http"
	authmw "github.com/perber/wiki/internal/http/middleware/auth"
	coreresearch "github.com/perber/wiki/internal/research"
)

type Routes struct {
	service     *coreresearch.Service
	authService *coreauth.AuthService
	apiToken    string
	apiPassword string
}

type RoutesConfig struct {
	Service     *coreresearch.Service
	AuthService *coreauth.AuthService
	APIToken    string
	APIPassword string
}

func NewRoutes(cfg RoutesConfig) *Routes {
	return &Routes{
		service:     cfg.Service,
		authService: cfg.AuthService,
		apiToken:    strings.TrimSpace(cfg.APIToken),
		apiPassword: strings.TrimSpace(cfg.APIPassword),
	}
}

func (r *Routes) RegisterRoutes(ctx httpinternal.RouterContext) {
	group := ctx.Base.Group("/api/research")
	group.Use(r.requireWriter(ctx))

	group.GET("/experiments", r.handleListExperiments)
	group.POST("/experiments", r.handleCreateExperiment)
	group.GET("/experiments/:id", r.handleGetExperiment)
	group.GET("/experiments/:id/context", r.handleGetExperimentContext)
	group.POST("/experiments/:id/events", r.handleAppendEvent)
	group.PATCH("/experiments/:id/status", r.handleUpdateStatus)
	group.POST("/experiments/:id/results", r.handleRecordResults)

	group.GET("/docs/search", r.handleSearchDocuments)
	group.GET("/docs/read", r.handleReadDocument)
	group.GET("/docs/recent", r.handleRecentDocuments)
}

func (r *Routes) requireWriter(ctx httpinternal.RouterContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		if r.acceptBearer(c) || r.acceptPassword(c) {
			c.Set("user", &coreauth.User{
				ID:       coreresearch.DefaultAgentUserID,
				Username: coreresearch.DefaultAgentUserID,
				Role:     coreauth.RoleEditor,
			})
			c.Next()
			return
		}

		if ctx.Opts.AuthDisabled {
			c.Set("user", &coreauth.User{
				ID:       "public-editor",
				Username: "public-editor",
				Role:     coreauth.RoleEditor,
			})
			c.Next()
			return
		}

		token, err := ctx.AuthCookies.ReadAccess(c)
		if err != nil || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid access token"})
			return
		}
		if r.authService == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "authentication service unavailable"})
			return
		}
		user, err := r.authService.ValidateToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		if !user.HasRole(coreauth.RoleAdmin) && !user.HasRole(coreauth.RoleEditor) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "editor or admin role required"})
			return
		}
		if requiresCSRF(c.Request.Method) {
			cookieToken, err := ctx.CSRFCookie.Read(c)
			if err != nil || cookieToken == "" {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "CSRF token missing"})
				return
			}
			headerToken := c.GetHeader("X-CSRF-Token")
			if headerToken == "" || subtle.ConstantTimeCompare([]byte(headerToken), []byte(cookieToken)) != 1 {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid CSRF token"})
				return
			}
		}
		c.Set("user", user)
		c.Next()
	}
}

func (r *Routes) acceptBearer(c *gin.Context) bool {
	if r.apiToken == "" {
		return false
	}
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return false
	}
	got := strings.TrimSpace(header[len("Bearer "):])
	return subtle.ConstantTimeCompare([]byte(got), []byte(r.apiToken)) == 1
}

func (r *Routes) acceptPassword(c *gin.Context) bool {
	if r.apiPassword == "" {
		return false
	}
	if got := strings.TrimSpace(c.GetHeader("X-Research-Password")); got != "" {
		return subtle.ConstantTimeCompare([]byte(got), []byte(r.apiPassword)) == 1
	}
	_, password, ok := c.Request.BasicAuth()
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(r.apiPassword)) == 1
}

func requiresCSRF(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func (r *Routes) handleCreateExperiment(c *gin.Context) {
	var req coreresearch.CreateExperimentInput
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithResearchError(c, http.StatusBadRequest, "invalid_request", "Invalid request")
		return
	}
	req.UserID = currentUserID(c)
	out, err := r.service.CreateExperiment(c.Request.Context(), req)
	if err != nil {
		respondWithResearchErrorForErr(c, err)
		return
	}
	status := http.StatusCreated
	if !out.Created {
		status = http.StatusOK
	}
	c.JSON(status, out)
}

func (r *Routes) handleAppendEvent(c *gin.Context) {
	var req coreresearch.AppendEventInput
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithResearchError(c, http.StatusBadRequest, "invalid_request", "Invalid request")
		return
	}
	req.ID = strings.TrimSpace(c.Param("id"))
	req.UserID = currentUserID(c)
	out, err := r.service.AppendEvent(c.Request.Context(), req)
	if err != nil {
		respondWithResearchErrorForErr(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (r *Routes) handleUpdateStatus(c *gin.Context) {
	var req coreresearch.UpdateStatusInput
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithResearchError(c, http.StatusBadRequest, "invalid_request", "Invalid request")
		return
	}
	req.ID = strings.TrimSpace(c.Param("id"))
	req.UserID = currentUserID(c)
	out, err := r.service.UpdateStatus(c.Request.Context(), req)
	if err != nil {
		respondWithResearchErrorForErr(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (r *Routes) handleRecordResults(c *gin.Context) {
	var req coreresearch.RecordResultsInput
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithResearchError(c, http.StatusBadRequest, "invalid_request", "Invalid request")
		return
	}
	req.ID = strings.TrimSpace(c.Param("id"))
	req.UserID = currentUserID(c)
	out, err := r.service.RecordResults(c.Request.Context(), req)
	if err != nil {
		respondWithResearchErrorForErr(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (r *Routes) handleGetExperiment(c *gin.Context) {
	out, err := r.service.GetExperiment(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		respondWithResearchErrorForErr(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (r *Routes) handleListExperiments(c *gin.Context) {
	out, err := r.service.ListExperiments(c.Request.Context(), c.Query("project"), c.Query("status"))
	if err != nil {
		respondWithResearchErrorForErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"experiments": out})
}

func (r *Routes) handleSearchDocuments(c *gin.Context) {
	out, err := r.service.SearchDocuments(c.Request.Context(), coreresearch.SearchDocumentsInput{
		Query:   c.Query("q"),
		Project: c.Query("project"),
		Kind:    c.Query("kind"),
		Offset:  queryInt(c, "offset"),
		Limit:   queryInt(c, "limit"),
	})
	if err != nil {
		respondWithResearchErrorForErr(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (r *Routes) handleReadDocument(c *gin.Context) {
	out, err := r.service.ReadDocument(c.Request.Context(), coreresearch.ReadDocumentInput{
		ID:   c.Query("id"),
		Path: c.Query("path"),
	})
	if err != nil {
		respondWithResearchErrorForErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"document": out})
}

func (r *Routes) handleRecentDocuments(c *gin.Context) {
	out, err := r.service.RecentDocuments(c.Request.Context(), coreresearch.RecentDocumentsInput{
		Project: c.Query("project"),
		Kind:    c.Query("kind"),
		Limit:   queryInt(c, "limit"),
	})
	if err != nil {
		respondWithResearchErrorForErr(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (r *Routes) handleGetExperimentContext(c *gin.Context) {
	out, err := r.service.GetExperimentContext(
		c.Request.Context(),
		strings.TrimSpace(c.Param("id")),
		c.Query("q"),
		queryInt(c, "limit"),
	)
	if err != nil {
		respondWithResearchErrorForErr(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func currentUserID(c *gin.Context) string {
	user := authmw.TryGetUser(c)
	if user == nil || strings.TrimSpace(user.ID) == "" {
		return coreresearch.DefaultAgentUserID
	}
	return user.ID
}

func queryInt(c *gin.Context, key string) int {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return n
}

func respondWithResearchErrorForErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, coreresearch.ErrInvalidInput):
		respondWithResearchError(c, http.StatusBadRequest, "invalid_research_input", err.Error())
	case errors.Is(err, coreresearch.ErrExperimentNotFound):
		respondWithResearchError(c, http.StatusNotFound, "experiment_not_found", "Experiment not found")
	case errors.Is(err, coreresearch.ErrDocumentNotFound):
		respondWithResearchError(c, http.StatusNotFound, "document_not_found", "Document not found")
	case errors.Is(err, coreresearch.ErrSearchUnavailable):
		respondWithResearchError(c, http.StatusServiceUnavailable, "search_unavailable", "Document search is unavailable")
	default:
		respondWithResearchError(c, http.StatusInternalServerError, "research_internal_error", err.Error())
	}
}

func respondWithResearchError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}
