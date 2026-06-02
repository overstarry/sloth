// Package api exposes the HTTP surface (gin) for the dashboard and operators:
// listing slow SQL, fetching diagnoses, and triggering on-demand analysis.
package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/overstarry/sloth/internal/model"
)

// Service is the application surface the API depends on.
type Service interface {
	TopSlowSQL(ctx context.Context, limit int32, instance string) ([]model.SlowSQL, error)
	LatestDiagnosis(ctx context.Context, fingerprint string) (*model.Diagnosis, error)
	// DiagnoseNow runs the full gather+LLM pipeline for one fingerprint.
	DiagnoseNow(ctx context.Context, fingerprint string) (*model.Diagnosis, error)
}

// Server wraps a gin engine bound to a Service.
type Server struct {
	svc    Service
	engine *gin.Engine
}

// New builds the router.
func New(svc Service) *Server {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.Use(gin.Recovery())
	s := &Server{svc: svc, engine: e}

	e.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	v1 := e.Group("/api/v1")
	{
		v1.GET("/slow-sql", s.listSlowSQL)
		v1.GET("/slow-sql/:fingerprint/diagnosis", s.getDiagnosis)
		v1.POST("/slow-sql/:fingerprint/diagnose", s.diagnose)
	}
	return s
}

// Handler returns the underlying http.Handler.
func (s *Server) Handler() http.Handler { return s.engine }

func (s *Server) listSlowSQL(c *gin.Context) {
	limit := int32(20)
	// Optional ?instance=<name> filter; empty returns every monitored target.
	instance := c.Query("instance")
	rows, err := s.svc.TopSlowSQL(c.Request.Context(), limit, instance)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (s *Server) getDiagnosis(c *gin.Context) {
	d, err := s.svc.LatestDiagnosis(c.Request.Context(), c.Param("fingerprint"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if d == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no diagnosis yet"})
		return
	}
	c.JSON(http.StatusOK, d)
}

func (s *Server) diagnose(c *gin.Context) {
	d, err := s.svc.DiagnoseNow(c.Request.Context(), c.Param("fingerprint"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}
