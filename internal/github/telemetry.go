package github

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type operationIDContextKey struct{}

type loggingTransport struct {
	base          http.RoundTripper
	logger        atomic.Pointer[slog.Logger]
	nextRequestID atomic.Uint64
}

func newLoggingTransport(base http.RoundTripper) *loggingTransport {
	return &loggingTransport{base: base}
}

func (t *loggingTransport) SetLogger(logger *slog.Logger) {
	t.logger.Store(logger)
}

func (t *loggingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	logger := t.logger.Load()
	if logger == nil || !logger.Enabled(request.Context(), slog.LevelDebug) {
		return t.base.RoundTrip(request)
	}

	requestID := t.nextRequestID.Add(1)
	operationID, _ := request.Context().Value(operationIDContextKey{}).(uint64)
	requestURL := safeURL(request.URL)
	requestAttrs := []any{
		"request_id", requestID,
		"operation_id", operationID,
		"method", request.Method,
		"url", requestURL,
		"host", request.URL.Host,
		"content_length", request.ContentLength,
	}
	logger.DebugContext(request.Context(), "http request", requestAttrs...)

	started := time.Now()
	response, err := t.base.RoundTrip(request)
	timeToHeaders := time.Since(started)
	if err != nil {
		logger.DebugContext(request.Context(), "http response",
			"request_id", requestID,
			"operation_id", operationID,
			"method", request.Method,
			"url", requestURL,
			"duration", timeToHeaders,
			"error", err,
		)
		return nil, err
	}

	response.Body = &loggingBody{
		ReadCloser:    response.Body,
		logger:        logger,
		ctx:           request.Context(),
		requestID:     requestID,
		operationID:   operationID,
		method:        request.Method,
		url:           requestURL,
		status:        response.Status,
		statusCode:    response.StatusCode,
		contentLength: response.ContentLength,
		contentType:   response.Header.Get("Content-Type"),
		location:      safeLocation(response.Header.Get("Location")),
		started:       started,
		timeToHeaders: timeToHeaders,
	}
	return response, nil
}

type loggingBody struct {
	io.ReadCloser
	logger        *slog.Logger
	ctx           context.Context
	requestID     uint64
	operationID   uint64
	method        string
	url           string
	status        string
	statusCode    int
	contentLength int64
	contentType   string
	location      string
	started       time.Time
	timeToHeaders time.Duration
	bytesRead     atomic.Int64
	complete      atomic.Bool
	once          sync.Once
}

func (b *loggingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytesRead.Add(int64(n))
	if err == io.EOF {
		b.complete.Store(true)
	}
	return n, err
}

func (b *loggingBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() {
		attrs := []any{
			"request_id", b.requestID,
			"operation_id", b.operationID,
			"method", b.method,
			"url", b.url,
			"status", b.status,
			"status_code", b.statusCode,
			"content_length", b.contentLength,
			"response_bytes", b.bytesRead.Load(),
			"content_type", b.contentType,
			"time_to_headers", b.timeToHeaders,
			"duration", time.Since(b.started),
			"body_complete", b.complete.Load(),
		}
		if b.location != "" {
			attrs = append(attrs, "location", b.location)
		}
		if err != nil {
			attrs = append(attrs, "close_error", err)
		}
		b.logger.DebugContext(b.ctx, "http response", attrs...)
	})
	return err
}

func safeLocation(location string) string {
	if location == "" {
		return ""
	}
	u, err := url.Parse(location)
	if err != nil {
		return "<invalid>"
	}
	return safeURL(u)
}

func safeURL(u *url.URL) string {
	copy := *u
	query := copy.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "signature") || lower == "sig" || lower == "jwt" {
			query.Set(key, "REDACTED")
		}
	}
	copy.RawQuery = query.Encode()
	copy.User = nil
	return copy.String()
}
