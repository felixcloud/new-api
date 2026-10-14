package helper

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

// forkUnmarshalAudioRequest(yunai fork) 让音频请求也接受 multipart 表单(语音转文字上传音频文件),
// 从表单里取 model、response_format、stream; 其余请求仍按 JSON 解析。
func forkUnmarshalAudioRequest(c *gin.Context, audioRequest *dto.AudioRequest) error {
	if !strings.Contains(c.Request.Header.Get("Content-Type"), gin.MIMEMultipartPOSTForm) {
		return common.UnmarshalBodyReusable(c, audioRequest)
	}
	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return fmt.Errorf("failed to parse audio form request: %w", err)
	}
	formData := url.Values(form.Value)
	c.Request.MultipartForm = form
	c.Request.PostForm = formData
	audioRequest.Model = formData.Get("model")
	audioRequest.ResponseFormat = formData.Get("response_format")
	if streamValue := strings.TrimSpace(formData.Get("stream")); streamValue != "" {
		stream, err := strconv.ParseBool(streamValue)
		if err != nil {
			return fmt.Errorf("invalid stream value: %w", err)
		}
		audioRequest.Stream = &stream
	}
	return nil
}
