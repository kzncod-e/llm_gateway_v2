package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"service/internal/gateway"
	"service/internal/observability"
	"service/internal/repository"
)

// ginSink adapts the engine's Sink interface to a Gin response.
type ginSink struct {
	c *gin.Context
}

func (s *ginSink) headers(m gateway.Meta) {
	h := s.c.Writer.Header()
	if m.Provider != "" {
		h.Set("X-Gateway-Provider", m.Provider)
		h.Set("X-Gateway-Model", m.Model)
	}
	h.Set("X-Gateway-Attempts", fmt.Sprint(m.Attempts))
}

func (s *ginSink) WriteError(m gateway.Meta, e *gateway.APIError) {
	if s.c.Request.Context().Err() != nil {
		return // client is gone
	}
	s.headers(m)
	if e.RetryAfter > 0 {
		s.c.Header("Retry-After", retryAfterSeconds(e.RetryAfter))
	}
	status := e.Status
	if status == 499 {
		status = http.StatusRequestTimeout
	}
	s.c.JSON(status, e.Body())
	s.c.Writer.Flush()
}

func (s *ginSink) WriteResponse(m gateway.Meta, resp *gateway.ChatCompletionResponse) {
	s.headers(m)
	b, err := json.Marshal(resp)
	if err != nil {
		s.c.JSON(http.StatusInternalServerError, errorBody("server_error", "internal_error", "failed to encode response"))
		return
	}
	s.c.Header("Content-Length", fmt.Sprint(len(b)))
	s.c.Data(http.StatusOK, "application/json", b)
	s.c.Writer.Flush()
}

func (s *ginSink) StartStream(m gateway.Meta) gateway.StreamWriter {
	s.headers(m)
	h := s.c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
	s.c.Writer.WriteHeader(http.StatusOK)
	s.c.Writer.Flush()
	return &sseWriter{w: s.c.Writer}
}

type sseWriter struct {
	w gin.ResponseWriter
}

func (w *sseWriter) Send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.w.Write(append(append([]byte("data: "), b...), '\n', '\n')); err != nil {
		return err
	}
	w.w.Flush()
	return nil
}

func (w *sseWriter) Done() error {
	if _, err := w.w.Write([]byte("data: [DONE]\n\n")); err != nil {
		return err
	}
	w.w.Flush()
	return nil
}

// decodeChat reads the chat request body with a read deadline (the server has
// no global ReadTimeout because it would cancel long-running streams).
func decodeChat(c *gin.Context) (*gateway.ChatCompletionRequest, *gateway.APIError) {
	rc := http.NewResponseController(c.Writer)
	_ = rc.SetReadDeadline(time.Now().Add(30 * time.Second))
	body, err := io.ReadAll(c.Request.Body)
	_ = rc.SetReadDeadline(time.Time{})
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			e := gateway.ErrInvalidRequest("request body too large")
			e.Status = http.StatusRequestEntityTooLarge
			return nil, e
		}
		return nil, gateway.ErrInvalidRequest("could not read request body")
	}
	var req gateway.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, gateway.ErrInvalidRequest("invalid JSON: " + err.Error())
	}
	if ae := req.Validate(); ae != nil {
		return nil, ae
	}
	return &req, nil
}

func (h *Handler) limitsFor(rpm, conc *int) (int, int) {
	r, cc := h.Cfg.DefaultTeamRPM, h.Cfg.DefaultTeamConcurrency
	if rpm != nil {
		r = *rpm
	}
	if conc != nil {
		cc = *conc
	}
	return r, cc
}

// ChatCompletions handles POST /v1/chat/completions.
func (h *Handler) ChatCompletions(c *gin.Context) {
	kp := c.MustGet(ctxKey).(*repository.KeyPrincipal)
	req, ae := decodeChat(c)
	if ae != nil {
		c.AbortWithStatusJSON(ae.Status, ae.Body())
		return
	}
	rpm, conc := h.limitsFor(kp.Team.RPMLimit, kp.Team.ConcurrencyLimit)
	keyID := kp.Key.ID
	call := &gateway.Call{
		RequestID: observability.GetRequestID(c), TeamID: kp.Team.ID, APIKeyID: &keyID, Source: "api",
		RPMLimit: rpm, Concurrency: conc, ProviderHint: c.GetHeader("X-Gateway-Provider"), Req: req,
	}
	h.Engine.Execute(c.Request.Context(), call, &ginSink{c: c})
}

// ListModels handles GET /v1/models.
func (h *Handler) ListModels(c *gin.Context) {
	ids := h.Engine.Catalog().Snapshot().PublicModels()
	data := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		data = append(data, gin.H{"id": id, "object": "model", "created": 0, "owned_by": "llm-gateway"})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

// Playground handles POST /api/v1/playground/chat: the same engine, attributed
// to a team the user may develop for, without exposing an API key.
func (h *Handler) Playground(c *gin.Context) {
	p := principal(c)
	teamID, err := uuid.Parse(c.Query("team_id"))
	if err != nil {
		h.fail(c, errInvalidTeamParam)
		return
	}
	if err := p.Require(teamID, "developer"); err != nil {
		h.fail(c, err)
		return
	}
	team, err := h.Repo.Team(c.Request.Context(), teamID)
	if err != nil {
		h.fail(c, err)
		return
	}
	if team.ArchivedAt != nil {
		h.fail(c, errArchived)
		return
	}
	req, ae := decodeChat(c)
	if ae != nil {
		c.AbortWithStatusJSON(ae.Status, ae.Body())
		return
	}
	rpm, conc := h.limitsFor(team.RPMLimit, team.ConcurrencyLimit)
	uid := p.User.ID
	call := &gateway.Call{
		RequestID: observability.GetRequestID(c), TeamID: team.ID, UserID: &uid, Source: "playground",
		RPMLimit: rpm, Concurrency: conc, ProviderHint: c.GetHeader("X-Gateway-Provider"), Req: req,
	}
	c.Set("team_id", team.ID.String())
	h.Engine.Execute(c.Request.Context(), call, &ginSink{c: c})
}
