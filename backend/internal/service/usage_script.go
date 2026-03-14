package service

import "time"

// UsageScript 用量脚本领域模型
type UsageScript struct {
	ID          int64     `json:"id"`
	BaseURLHost string    `json:"base_url_host"`
	AccountType string    `json:"account_type"`
	Script      string    `json:"script"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
