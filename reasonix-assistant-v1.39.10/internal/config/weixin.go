package config

// WeixinBotConfig 微信 iLink Bot 配置。
type WeixinBotConfig struct {
	Enabled   bool   `toml:"enabled"`
	FinalOnly bool   `toml:"final_only"` // Only final replies and required interactions reach chat.
	AccountID string `toml:"account_id"`
	TokenEnv  string `toml:"token_env"` // 环境变量名，如 WEIXIN_BOT_TOKEN
	APIBase   string `toml:"api_base"`  // iLink API base URL
}
