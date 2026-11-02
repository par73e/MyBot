package assistant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"
)

type alertScopeSchedule struct {
	Weekday string `json:"weekday"`
	Weekend string `json:"weekend"`
}

// The schedule is opt-in per instance; absent settings preserve existing
// behaviour for other instances. Invalid settings fail closed to city alerts.
func (s AlertService) alertInScope(a OfficialAlert) bool {
	raw, err := os.ReadFile(filepath.Join(s.Home, "assistant", "alert-scope.json"))
	if os.IsNotExist(err) {
		return true
	}
	var cfg alertScopeSchedule
	if err != nil || json.Unmarshal(raw, &cfg) != nil || !slices.Contains(alertAreas, cfg.Weekday) || !slices.Contains(alertAreas, cfg.Weekend) {
		return alertAppliesToDistrict(a.Area, "")
	}
	district := cfg.Weekday
	weekday := s.now().In(mustShanghai()).Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		district = cfg.Weekend
	}
	return alertAppliesToDistrict(a.Area, district)
}

// alertAppliesToDistrict is deliberately conservative: an unknown location
// receives only municipality-wide alerts. A central-city alert is narrower
// than the municipality and needs a known district.
func alertAppliesToDistrict(area, district string) bool {
	if area == "全市" {
		return true
	}
	if !slices.Contains(alertAreas, district) {
		return false
	}
	if area == "中心城区" {
		return slices.Contains([]string{"黄浦区", "徐汇区", "长宁区", "静安区", "普陀区", "虹口区", "杨浦区"}, district)
	}
	return area == district
}
