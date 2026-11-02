package helper

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const (
	contextKeyOmitSSEEvent = "omit_sse_event"
	headerNoSSEEvent       = "X-No-SSE-Event"
)

type omitSSEEventContextKey struct{}

// ForkWrapV0CompatHandler(yunai fork) 在进入 Gin 路由匹配前将 /v0/* 透明改写为 /v1/*,
// 并为 /v0/*、X-No-SSE-Event 请求头或 no_event/sse_event 查询参数打上省略 SSE "event:" 行的标记。
// 同时剥离自定义兼容头和查询参数, 避免透传给上游渠道。
func ForkWrapV0CompatHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r != nil && r.URL != nil {
			omitEvent := false

			if after, ok := strings.CutPrefix(r.URL.Path, "/v0"); ok && (after == "" || strings.HasPrefix(after, "/")) {
				r.URL.Path = "/v1" + after
				if rawAfter, rawOK := strings.CutPrefix(r.URL.RawPath, "/v0"); rawOK && (rawAfter == "" || strings.HasPrefix(rawAfter, "/")) {
					r.URL.RawPath = "/v1" + rawAfter
				}
				omitEvent = true
			}

			if r.URL.RawQuery != "" && (strings.Contains(r.URL.RawQuery, "no_event") || strings.Contains(r.URL.RawQuery, "sse_event")) {
				query := r.URL.Query()
				if isTruthyFlag(query.Get("no_event")) || isFalsyFlag(query.Get("sse_event")) {
					omitEvent = true
				}
				query.Del("no_event")
				query.Del("sse_event")
				r.URL.RawQuery = query.Encode()
			}

			if isTruthyFlag(r.Header.Get(headerNoSSEEvent)) {
				omitEvent = true
			}
			r.Header.Del(headerNoSSEEvent)

			if omitEvent {
				r.RequestURI = r.URL.RequestURI()
				r = r.WithContext(context.WithValue(r.Context(), omitSSEEventContextKey{}, true))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ForkShouldOmitSSEEvent(yunai fork) 判断当前请求是否需要省略 Responses SSE 的 "event:" 头。
func ForkShouldOmitSSEEvent(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if c.GetBool(contextKeyOmitSSEEvent) {
		return true
	}
	if c.Request == nil {
		return false
	}
	if omit, ok := c.Request.Context().Value(omitSSEEventContextKey{}).(bool); ok && omit {
		c.Set(contextKeyOmitSSEEvent, true)
		return true
	}
	if c.Request.URL != nil {
		if after, ok := strings.CutPrefix(c.Request.URL.Path, "/v0"); ok && (after == "" || strings.HasPrefix(after, "/")) {
			c.Set(contextKeyOmitSSEEvent, true)
			return true
		}
	}
	if isTruthyFlag(c.GetHeader(headerNoSSEEvent)) || isTruthyFlag(c.Query("no_event")) || isFalsyFlag(c.Query("sse_event")) {
		c.Set(contextKeyOmitSSEEvent, true)
		return true
	}
	return false
}

func forkResponseChunkDataOnly(c *gin.Context, data string) error {
	event := common.CustomEvent{Data: fmt.Sprintf("data: %s", data)}
	if err := event.Render(c.Writer); err != nil {
		return err
	}
	return FlushWriter(c)
}

func isTruthyFlag(val string) bool {
	v := strings.TrimSpace(val)
	return v == "1" || strings.EqualFold(v, "true")
}

func isFalsyFlag(val string) bool {
	v := strings.TrimSpace(val)
	return v == "0" || strings.EqualFold(v, "false")
}
