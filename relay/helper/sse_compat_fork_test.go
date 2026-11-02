package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForkV0CompatAndResponseChunkData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := `{"type":"response.completed","sequence_number":31}`
	streamResp := dto.ResponsesStreamResponse{Type: "response.completed"}

	tests := []struct {
		name            string
		target          string
		headers         map[string]string
		wantPath        string
		wantRequestURI  string
		wantHeaderEmpty bool
		wantBody        string
	}{
		{
			name:            "v1 default keeps event line",
			target:          "/v1/responses",
			wantPath:        "/v1/responses",
			wantRequestURI:  "/v1/responses",
			wantHeaderEmpty: true,
			wantBody:        "event: response.completed\ndata: " + payload + "\n\n",
		},
		{
			name:            "v0 path rewrites to v1 and omits event line",
			target:          "/v0/responses?foo=bar",
			wantPath:        "/v1/responses",
			wantRequestURI:  "/v1/responses?foo=bar",
			wantHeaderEmpty: true,
			wantBody:        "data: " + payload + "\n\n",
		},
		{
			name:            "v1 with no_event query omits event line and strips param",
			target:          "/v1/responses?no_event=1&foo=bar",
			wantPath:        "/v1/responses",
			wantRequestURI:  "/v1/responses?foo=bar",
			wantHeaderEmpty: true,
			wantBody:        "data: " + payload + "\n\n",
		},
		{
			name:            "v1 with sse_event=0 query omits event line and strips param",
			target:          "/v1/responses?sse_event=0",
			wantPath:        "/v1/responses",
			wantRequestURI:  "/v1/responses",
			wantHeaderEmpty: true,
			wantBody:        "data: " + payload + "\n\n",
		},
		{
			name:            "v1 with X-No-SSE-Event header omits event line and strips header",
			target:          "/v1/responses",
			headers:         map[string]string{"X-No-SSE-Event": "1"},
			wantPath:        "/v1/responses",
			wantRequestURI:  "/v1/responses",
			wantHeaderEmpty: true,
			wantBody:        "data: " + payload + "\n\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine := gin.New()
			var gotPath, gotRequestURI, gotHeader string
			engine.POST("/v1/responses", func(c *gin.Context) {
				gotPath = c.Request.URL.Path
				gotRequestURI = c.Request.RequestURI
				gotHeader = c.GetHeader(headerNoSSEEvent)
				SetEventStreamHeaders(c)
				require.NoError(t, ResponseChunkData(c, streamResp, payload))
			})

			handler := ForkWrapV0CompatHandler(engine)
			req := httptest.NewRequest(http.MethodPost, tc.target, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusOK, recorder.Code)
			assert.Equal(t, tc.wantPath, gotPath)
			assert.Equal(t, tc.wantRequestURI, gotRequestURI)
			if tc.wantHeaderEmpty {
				assert.Empty(t, gotHeader)
			}
			assert.Equal(t, tc.wantBody, recorder.Body.String())
		})
	}
}
