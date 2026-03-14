package service

// ScriptUsageWindow 脚本返回的单个用量窗口
type ScriptUsageWindow struct {
	Name        string   `json:"name"`
	Utilization float64  `json:"utilization"` // 0.0~1.0+
	ResetsAt    *int64   `json:"resets_at"`   // unix timestamp, optional
	Used        *float64 `json:"used"`
	Limit       *float64 `json:"limit"`
	Unit        string   `json:"unit"` // tokens/requests/credits
}

// ScriptUsageResult 脚本执行结果
type ScriptUsageResult struct {
	Windows []ScriptUsageWindow `json:"windows"`
	Error   string              `json:"error,omitempty"`
}
