package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestWeixinFinalOnlyPersistsAcrossConfigRender(t *testing.T) {
	cfg := Default()
	if cfg.Bot.Weixin.FinalOnly {
		t.Fatal("existing installations must retain their output policy")
	}
	if _, err := toml.Decode("[bot.weixin]\nfinal_only = true\n", &cfg); err != nil {
		t.Fatal(err)
	}
	var decoded Config
	if _, err := toml.Decode(RenderTOML(cfg), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Bot.Weixin.FinalOnly {
		t.Fatal("final-only output policy was lost on config save")
	}
}
