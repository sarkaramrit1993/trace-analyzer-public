package ingestion

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/models"
)

// SpanHandler is called for each span received from the SSE stream
type SpanHandler func(span *models.Span) error

// SSEClient connects to an SSE endpoint and receives spans
type SSEClient struct {
	endpoint       string
	handler        SpanHandler
	client         *http.Client
	ctx            context.Context
	cancel         context.CancelFunc
	wg             waitGroupWrapper
	reconnectDelay time.Duration

	spansReceived atomic.Int64
	errors        atomic.Int64
}

// waitGroupWrapper wraps sync.WaitGroup to avoid copying issues
type waitGroupWrapper struct {
	wg sync.WaitGroup
}

// NewSSEClient creates a new SSE client
func NewSSEClient(endpoint string, handler SpanHandler) *SSEClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &SSEClient{
		endpoint:       endpoint,
		handler:        handler,
		client:         &http.Client{Timeout: 0},
		ctx:            ctx,
		cancel:         cancel,
		reconnectDelay: 5 * time.Second,
	}
}

// Start begins consuming from the SSE stream
func (c *SSEClient) Start() error {
	c.wg.wg.Add(1)
	go c.consumeLoop()
	log.Info().Str("endpoint", c.endpoint).Msg("SSE client started")
	return nil
}

// Stop gracefully shuts down the SSE client
func (c *SSEClient) Stop() {
	c.cancel()
	c.wg.wg.Wait()
	log.Info().Msg("SSE client stopped")
}

// Stats returns current metrics
func (c *SSEClient) Stats() (received int64, errors int64) {
	return c.spansReceived.Load(), c.errors.Load()
}

func (c *SSEClient) consumeLoop() {
	defer c.wg.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			err := c.connect()
			if err != nil {
				log.Error().Err(err).Msg("SSE connection error")
				c.errors.Add(1)

				select {
				case <-c.ctx.Done():
					return
				case <-time.After(c.reconnectDelay):
					log.Info().Msg("Reconnecting to SSE stream...")
				}
			}
		}
	}
}

func (c *SSEClient) connect() error {
	req, err := http.NewRequestWithContext(c.ctx, "GET", c.endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	log.Info().Str("endpoint", c.endpoint).Msg("Connected to SSE stream")

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var eventType string
	var eventData strings.Builder

	for scanner.Scan() {
		select {
		case <-c.ctx.Done():
			return nil
		default:
		}

		line := scanner.Text()

		if line == "" {
			if eventData.Len() > 0 {
				c.processEvent(eventType, eventData.String())
				eventType = ""
				eventData.Reset()
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)
			eventData.WriteString(data)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}

	return fmt.Errorf("stream closed")
}

func (c *SSEClient) processEvent(eventType, data string) {
	switch eventType {
	case "trace_batch":
		if data != "" {
			c.processTraceBatch(data)
		}
	case "ping":
		// Keepalive ping - ignore but log for debugging
		log.Debug().Msg("Received keepalive ping from SSE stream")
	case "start", "init", "end", "query_complete":
		// Stream lifecycle events - ignore
	case "":
		// No event type specified, try as trace_batch
		if data != "" {
			c.processTraceBatch(data)
		}
	}
}

func (c *SSEClient) processTraceBatch(data string) {
	// Try parsing as array of spans first (trace_batch format)
	var spans []*models.Span
	if err := json.Unmarshal([]byte(data), &spans); err == nil && len(spans) > 0 {
		var successCount, errorCount int64
		for _, span := range spans {
			if err := c.handler(span); err != nil {
				log.Warn().Err(err).Str("trace_id", span.TraceID).Msg("Handler error")
				errorCount++
				continue
			}
			successCount++
		}
		c.spansReceived.Add(successCount)
		if errorCount > 0 {
			c.errors.Add(errorCount)
		}
		return
	}

	// Try parsing as single span (backwards compatibility)
	var span models.Span
	if err := json.Unmarshal([]byte(data), &span); err != nil {
		log.Warn().Err(err).Str("data", truncate(data, 100)).Msg("Failed to parse span data")
		c.errors.Add(1)
		return
	}

	if err := c.handler(&span); err != nil {
		log.Warn().Err(err).Str("trace_id", span.TraceID).Msg("Handler error")
		c.errors.Add(1)
		return
	}

	c.spansReceived.Add(1)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
