#!/usr/bin/env python3
"""Create a build overlay for the acceptance-only weather transport. NEVER deploy it."""
import json
from pathlib import Path
import sys

root=Path(__file__).resolve().parents[1]
out=Path(sys.argv[1]).resolve();out.mkdir(parents=True,exist_ok=True)
original=root/'internal/assistant/weather_http.go'
source=original.read_text();start=source.index('func (s WeatherService) httpFetch(');end=source.index('\nfunc (s WeatherService) part(',start)
source=source[:start]+'''func (s WeatherService) httpFetch(ctx context.Context, path string) (json.RawMessage, error) {
 kind := ""
 switch {
 case strings.Contains(path, "/geo/"): kind="geo"
 case strings.Contains(path, "/historical/"): kind="history"
 case strings.Contains(path, "/weatheralert/"): kind="alerts"
 case strings.Contains(path, "/airquality/v1/current/"): kind="air_current"
 case strings.Contains(path, "/airquality/v1/hourly/"): kind="air_hourly"
 case strings.Contains(path, "/airquality/v1/daily/"): kind="air_daily"
 case strings.Contains(path, "/weather/v1/current/"): kind="current"
 case strings.Contains(path, "/weather/v1/hourly/"): kind="hourly"
 case strings.Contains(path, "/weather/v1/daily/"): kind="daily"
 case strings.Contains(path, "/minutely/"): kind="minutely"
 case strings.Contains(path, "/indices/"): kind="indices"
 case strings.Contains(path, "/astronomy/"): kind="astronomy"
 default: return nil,errors.New("unexpected fixture weather path")
 }
 return os.ReadFile(filepath.Join(os.Getenv("WEATHER_TEST_DATA_DIR"),kind+".json"))
}
''' +source[end:]
for imp in ['"compress/gzip"','"fmt"','"io"','"net/http"']:
 source=source.replace('\n\t'+imp,'')
source=source.replace('"os"','"os"\n "path/filepath"\n "strings"')
replacement=out/'weather_http.go';replacement.write_text(source)
(out/'overlay.json').write_text(json.dumps({'Replace':{str(original):str(replacement)}}))
print(out/'overlay.json')
