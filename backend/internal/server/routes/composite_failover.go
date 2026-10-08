package routes

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const compositeCandidatesKey = "composite_failover_candidates"

// Each provider still uses its existing protocol adapter, scheduler and billing
// checks. Only an uncommitted capacity/upstream error may advance to another
// owner. In particular a flushed SSE response can never be replayed.
func compositeFailoverHandler(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, _ := c.Get(compositeCandidatesKey)
		candidates, _ := value.([]service.CompositeRouteDecision)
		if len(candidates) < 2 || c.Writer.Written() {
			next(c)
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Failed to read request body"}})
			return
		}
		baseRequest, originalWriter := c.Request, c.Writer
		baseKeys := copyCompositeContextKeys(c.Keys)
		baseErrors := c.Errors
		defer func() { c.Writer = originalWriter }()
		for index, candidate := range candidates {
			if index > 0 {
				// Clear attempt-local account attribution, terminal-error flags and policy
				// state, but retain upstream history for the outer operations logger.
				history, hasHistory := c.Get(service.OpsUpstreamErrorsKey)
				c.Keys = copyCompositeContextKeys(baseKeys)
				if hasHistory {
					c.Set(service.OpsUpstreamErrorsKey, history)
				}
				c.Errors = baseErrors
			}
			c.Request = baseRequest.WithContext(service.WithCompositeRouteDecision(baseRequest.Context(), candidate))
			requestmodel.ResetRequestBody(c.Request, body)
			writer := newCompositeAttemptWriter(originalWriter)
			c.Writer = writer
			next(c)
			if index == len(candidates)-1 || originalWriter.Written() || c.Request.Context().Err() != nil || !retryCompositeAttempt(c, writer) {
				writer.commit()
				return
			}
		}
	}
}

func copyCompositeContextKeys(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func retryCompositeAttempt(c *gin.Context, w *compositeAttemptWriter) bool {
	if c.GetBool(service.OpsClientBusinessLimitedKey) {
		return false
	}
	switch w.Status() {
	case 429, 502, 503, 504:
	default:
		return false
	}
	// Do not turn user/group rate limits, quota or billing denials into retries.
	// Upstream failures are marked by the provider adapter; selection exhaustion
	// has no upstream response and is recognized by its existing error message.
	if c.GetBool(service.OpsRoutingCapacityLimitedKey) {
		return true
	}
	if status := c.GetInt(service.OpsUpstreamStatusCodeKey); status >= 400 {
		return true
	}
	message := strings.ToLower(w.body.String())
	return strings.Contains(message, "no available accounts") ||
		strings.Contains(message, "no available openai accounts") ||
		strings.Contains(message, "all available accounts")
}

// Buffer only error responses, and bound the buffer. Success (including SSE)
// is forwarded immediately. A flushed or oversized error becomes terminal.
type compositeAttemptWriter struct {
	gin.ResponseWriter
	header    http.Header
	status    int
	size      int
	body      bytes.Buffer
	committed bool
}

func newCompositeAttemptWriter(w gin.ResponseWriter) *compositeAttemptWriter {
	return &compositeAttemptWriter{ResponseWriter: w, header: w.Header().Clone(), status: http.StatusOK, size: -1}
}
func (w *compositeAttemptWriter) Header() http.Header { return w.header }
func (w *compositeAttemptWriter) Status() int         { return w.status }
func (w *compositeAttemptWriter) Size() int           { return w.size }
func (w *compositeAttemptWriter) Written() bool       { return w.size >= 0 }
func (w *compositeAttemptWriter) WriteHeader(status int) {
	if !w.Written() && status > 0 {
		w.status = status
	}
}
func (w *compositeAttemptWriter) WriteHeaderNow() {
	if w.size < 0 {
		w.size = 0
	}
	if w.status < 400 {
		w.commit()
	}
}
func (w *compositeAttemptWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	if !w.committed && w.body.Len()+len(data) > 64*1024 {
		w.commit()
	}
	var n int
	var err error
	if w.committed {
		n, err = w.ResponseWriter.Write(data)
	} else {
		n, err = w.body.Write(data)
	}
	w.size += n
	return n, err
}
func (w *compositeAttemptWriter) WriteString(data string) (int, error) { return w.Write([]byte(data)) }
func (w *compositeAttemptWriter) Flush()                               { w.commit(); w.ResponseWriter.Flush() }
func (w *compositeAttemptWriter) commit() {
	if w.committed {
		return
	}
	w.committed = true
	dst := w.ResponseWriter.Header()
	for key := range dst {
		delete(dst, key)
	}
	for key, values := range w.header {
		dst[key] = append([]string(nil), values...)
	}
	w.ResponseWriter.WriteHeader(w.status)
	w.ResponseWriter.WriteHeaderNow()
	if w.size < 0 {
		w.size = 0
	}
	if w.body.Len() > 0 {
		_, _ = w.ResponseWriter.Write(w.body.Bytes())
		w.body.Reset()
	}
}
