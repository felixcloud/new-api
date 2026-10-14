package ratio_setting

// forkResolveCompletionRatio 是 yunai fork 的补全倍率规则: 配置了就以配置为准, 上游对部分模型家族
// 锁定的内置补全倍率(claude 5、gpt-5 8 …)只在没配置时兜底, 且一律不标锁定(管理界面可改)。
// 外部业务系统按价格卡计费, 这里倍率全为 1, new-api 的额度只用来按 Token 数兜住泄露 Key 的损失;
// 锁定的倍率会让输出 Token 按 5–8 倍扣额度。name 已经过 FormatMatchingModelName。
func forkResolveCompletionRatio(name string, configured *float64) (CompletionRatioInfo, bool) {
	if configured != nil {
		return CompletionRatioInfo{Ratio: *configured}, true
	}
	hardCodedRatio, _ := getHardcodedCompletionModelRatio(name)
	return CompletionRatioInfo{Ratio: hardCodedRatio}, true
}
