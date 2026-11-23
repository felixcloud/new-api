package common

import "github.com/QuantumNous/new-api/relaykit/dto"

// yunai fork: 同步出图链路(/v1/images/generations、/v1/images/edits)采集的计费事实,
// 无论 ratio 还是 tiered_expr 模式都写入消费日志 other 供外部业务系统计价,
// 不改变 new-api 自身的额度结算逻辑。
type ImageBillingFacts struct {
	// ActualImageCount 上游响应实际返回的成功图片张数(含组图与图层拆分); 0 表示退回请求张数
	ActualImageCount int
	// ReferenceImages 请求中传入的参考图张数(image / images / image_urls / multipart 文件)
	ReferenceImages int
	// Size 请求参数 size(如 1024x1024、1K、1.5K、2K、auto)
	Size string
	// Quality 请求参数 quality(如 standard、hd、high)
	Quality string
	// OutputSizes 上游响应每张成功输出图携带的 size 字符串(如 "2048x2048"); 空串表示该图未带 size
	OutputSizes []string
}

// forkRecordImageCount 在 UpdateImageCount 时同步记录非零实际张数, 使 ratio 模式下
// 其他出图适配器(Gemini / Vertex / SiliconFlow 等)也能把实际张数落入日志 other。
func (info *RelayInfo) forkRecordImageCount(count int64) {
	if info == nil || info.ImageBillingFacts == nil || count <= 0 || count > int64(dto.MaxImageN) {
		return
	}
	if len(info.ImageBillingFacts.OutputSizes) == 0 {
		info.ImageBillingFacts.ActualImageCount = int(count)
	}
}
