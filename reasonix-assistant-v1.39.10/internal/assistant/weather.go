package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type WeatherConfig struct {
	Enabled        bool              `json:"enabled"`
	CredentialFile string            `json:"credential_file"`
	LocationsFile  string            `json:"locations_file"`
	MorningEnabled bool              `json:"morning_enabled"`
	WakeTimes      map[string]string `json:"wake_times"`
}
type weatherCredentials struct {
	Host        string `json:"api_host"`
	Developer   string `json:"developer_id"`
	Project     string `json:"project_id"`
	KeyID       string `json:"credential_id"`
	PrivateFile string `json:"private_key_file"`
}
type WeatherPlace struct {
	Name      string  `json:"name"`
	Region    string  `json:"region,omitempty"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}
type weatherLocations struct {
	Timezone string                  `json:"timezone"`
	Defaults map[string]string       `json:"default_by_weekday"`
	Places   map[string]WeatherPlace `json:"places"`
}
type WeatherService struct {
	Home  string
	Fetch func(context.Context, string) (json.RawMessage, error)
	Clock func() time.Time
}

func NewWeatherService(home string) WeatherService { return WeatherService{Home: home} }
func (s WeatherService) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}
func readWeatherJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return errors.New("weather host configuration unavailable")
	}
	if json.Unmarshal(raw, v) != nil {
		return errors.New("invalid weather host configuration")
	}
	return nil
}
func (s WeatherService) Config() (WeatherConfig, error) {
	var c WeatherConfig
	err := readWeatherJSON(filepath.Join(s.Home, "assistant", "weather.json"), &c)
	if err != nil {
		return c, err
	}
	if !c.Enabled || !filepath.IsAbs(c.CredentialFile) || !filepath.IsAbs(c.LocationsFile) {
		return c, errors.New("weather is not configured/enabled")
	}
	if len(c.WakeTimes) != 7 {
		return c, errors.New("configure all seven wake times")
	}
	for _, day := range weatherWeekdays {
		value := c.WakeTimes[day]
		if !regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`).MatchString(value) {
			return c, errors.New("wake times must use HH:MM")
		}
	}
	return c, nil
}
func (s WeatherService) credentials() (weatherCredentials, error) {
	var c weatherCredentials
	cfg, err := s.Config()
	if err != nil {
		return c, err
	}
	if err = readWeatherJSON(cfg.CredentialFile, &c); err != nil {
		return c, err
	}
	if !regexp.MustCompile(`^[a-z0-9-]+\.[a-z0-9-]+\.qweatherapi\.com$`).MatchString(c.Host) || c.Developer == "" || c.Project == "" || c.KeyID == "" || !filepath.IsAbs(c.PrivateFile) {
		return c, errors.New("invalid weather credentials")
	}
	return c, nil
}
func (s WeatherService) locations() (weatherLocations, error) {
	var l weatherLocations
	c, err := s.Config()
	if err != nil {
		return l, err
	}
	if err = readWeatherJSON(c.LocationsFile, &l); err != nil {
		return l, err
	}
	if l.Timezone != "Asia/Shanghai" {
		return l, errors.New("weather defaults require Asia/Shanghai")
	}
	for _, day := range weatherWeekdays {
		if _, ok := l.Places[l.Defaults[day]]; !ok {
			return l, errors.New("missing default weather place")
		}
	}
	for _, p := range l.Places {
		if !validWeatherCoordinates(p.Latitude, p.Longitude) {
			return l, errors.New("invalid configured weather coordinates")
		}
	}
	return l, nil
}
func validWeatherCoordinates(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && !math.IsInf(lat, 0) && !math.IsInf(lon, 0) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

var weatherWeekdays = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
