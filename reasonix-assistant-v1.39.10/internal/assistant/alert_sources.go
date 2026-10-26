package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type alertSourceResult struct{ Count, DetailErrors int }
type alertEmit func(OfficialAlert) error

func (s AlertService) readShanghai(ctx context.Context, emit alertEmit) (alertSourceResult, error) {
	raw, err := s.fetch(ctx, alertShanghai, "https://sh.weather.com.cn/zhyj/")
	if err != nil {
		return alertSourceResult{}, err
	}
	var rows []struct{ Province, City, Town, Type, Level, Time, Content string }
	if err = alertJSON(raw, "alarm_callback(", ")", &rows); err != nil || rows == nil {
		return alertSourceResult{}, errors.New("Shanghai alert list is invalid; previous state retained")
	}
	if len(rows) > 200 {
		return alertSourceResult{}, errors.New("Shanghai alert list exceeds capacity")
	}
	result := alertSourceResult{}
	for _, r := range rows {
		if !strings.Contains(r.Province, "上海") {
			return result, errors.New("Shanghai alert list includes an unexpected province")
		}
		at, e := alertTime(r.Time)
		if e != nil {
			return result, e
		}
		title := r.Province + r.Town + r.Type + r.Level + "预警"
		area := alertArea(r.Town, r.Content)
		if r.Town == "" {
			area = alertArea(title, r.Content)
		}
		a, e := canonicalAlert(OfficialAlert{Identifier: "sh-" + mailHash(title+":"+r.Time), Area: area, Hazard: r.Type, Level: r.Level, PublishedAt: at, Body: r.Content, Cancel: explicitAlertCancel(title, r.Content), Source: "shanghai", URL: "https://sh.weather.com.cn/zhyj/"}, title, s.now())
		if e != nil {
			return result, e
		}
		if e = emit(a); e != nil {
			return result, e
		}
		result.Count++
	}
	return result, nil
}

type nmcAlertRow struct {
	ID     string `json:"alertid"`
	Issued string `json:"issuetime"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

func (s AlertService) readNMC(ctx context.Context, emit alertEmit) (alertSourceResult, error) {
	result := alertSourceResult{}
	for page := 1; page <= 10; page++ {
		q := url.Values{"pageNo": {fmt.Sprint(page)}, "pageSize": {"20"}, "signaltype": {""}, "signallevel": {""}, "province": {"上海市"}}
		raw, err := s.fetch(ctx, alertNMC+"/rest/findAlarm?"+q.Encode(), alertNMC+"/f/alarm.html")
		if err != nil {
			return result, err
		}
		var data struct {
			Code *int `json:"code"`
			Data struct {
				Page struct {
					PageNo, PageSize, Count, TotalPage int
					List                               []nmcAlertRow
				}
			}
		}
		if err = alertJSON(raw, "", "", &data); err != nil || data.Code == nil || *data.Code != 0 || data.Data.Page.List == nil {
			return result, errors.New("NMC alert response is not a successful complete list")
		}
		p := data.Data.Page
		if p.PageNo != page || p.Count < 0 || p.Count > 200 || p.TotalPage > 10 || len(p.List) > 20 || p.Count > 0 && len(p.List) == 0 {
			return result, errors.New("NMC alert pagination is inconsistent or exceeds capacity")
		}
		for _, r := range p.List {
			if err = s.emitNMC(ctx, r, emit, &result); err != nil {
				return result, err
			}
		}
		if p.TotalPage <= page {
			return result, nil
		}
	}
	return result, errors.New("NMC alert pagination incomplete")
}
func (s AlertService) emitNMC(ctx context.Context, r nmcAlertRow, emit alertEmit, result *alertSourceResult) error {
	if !alertNMCID.MatchString(r.ID) || r.URL != "/publish/alarm/"+r.ID+".html" || !strings.Contains(r.Title, "上海") {
		return errors.New("NMC alert item has unexpected identity or detail URL")
	}
	at, err := alertTime(r.Issued)
	if err != nil {
		return err
	}
	a := OfficialAlert{Identifier: r.ID, PublishedAt: at, Source: "nmc", URL: alertNMC + r.URL}
	// A detail outage cannot silence a valid official list publication.
	raw, e := s.fetch(ctx, a.URL, alertNMC+"/f/alarm.html")
	if e == nil {
		a.Body, e = nmcAlertBody(raw)
	}
	if e != nil {
		result.DetailErrors++
	}
	a.Cancel = explicitAlertCancel(r.Title, a.Body)
	a, err = canonicalAlert(a, r.Title, s.now())
	if err != nil {
		return err
	}
	if err = emit(a); err != nil {
		return err
	}
	result.Count++
	return nil
}
func (s AlertService) readProduct(ctx context.Context, emit alertEmit) (alertSourceResult, error) {
	raw, err := s.fetch(ctx, alertProduct+"/alarm/grepalarm_cn.php", "https://www.weather.com.cn/alarm/")
	if err != nil {
		return alertSourceResult{}, err
	}
	var data struct {
		Count json.RawMessage `json:"count"`
		Data  [][]string      `json:"data"`
	}
	if err = alertJSON(raw, "var alarminfo=", "", &data); err != nil || data.Count == nil || data.Data == nil || len(data.Data) > 5000 {
		return alertSourceResult{}, errors.New("national alert list is invalid or oversized")
	}
	countText := strings.Trim(string(data.Count), "\"")
	count, countErr := strconv.Atoi(countText)
	if countErr != nil || count != len(data.Data) {
		return alertSourceResult{}, errors.New("national alert count does not match complete list")
	}
	rows := [][]string{}
	for _, r := range data.Data {
		if len(r) < 2 {
			return alertSourceResult{}, errors.New("national alert list contains malformed entries")
		}
		if strings.HasPrefix(r[0], "上海") {
			rows = append(rows, r)
		}
	}
	if len(rows) > 200 {
		return alertSourceResult{}, errors.New("national Shanghai alert coverage exceeds capacity")
	}
	// Updated alerts first, so a bounded run cannot bury the latest publication.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i][1] > rows[j][1] })
	result := alertSourceResult{}
	for _, r := range rows {
		if !alertFile.MatchString(r[1]) {
			return result, errors.New("invalid official alert filename")
		}
		a, e := s.productDetail(ctx, r[1])
		if e != nil {
			result.DetailErrors++
			continue
		}
		if e = emit(a); e != nil {
			return result, e
		}
		result.Count++
	}
	if result.DetailErrors > 0 {
		return result, errors.New("some Shanghai alert details unavailable; coverage is incomplete")
	}
	return result, nil
}
func (s AlertService) productDetail(ctx context.Context, file string) (OfficialAlert, error) {
	var a OfficialAlert
	if !alertFile.MatchString(file) {
		return a, errors.New("invalid official alert filename")
	}
	raw, err := s.fetch(ctx, alertProduct+"/alarm/webdata/"+file, "https://www.weather.com.cn/alarm/")
	if err != nil {
		return a, err
	}
	var d struct {
		Head       string `json:"head"`
		Province   string `json:"PROVINCE"`
		City       string `json:"CITY"`
		Station    string `json:"STATIONNAME"`
		Hazard     string `json:"SIGNALTYPE"`
		Level      string `json:"SIGNALLEVEL"`
		Issued     string `json:"ISSUETIME"`
		Expires    string `json:"RELIEVETIME"`
		Body       string `json:"ISSUECONTENT"`
		Identifier string `json:"identifier"`
		References string `json:"references"`
		MsgType    string `json:"msgType"`
	}
	if err = alertJSON(raw, "var alarminfo=", "", &d); err != nil {
		return a, err
	}
	if !strings.Contains(d.Province, "上海") || !strings.HasPrefix(d.Identifier, "31") || d.Body == "" {
		return a, errors.New("national alert detail does not identify an official Shanghai warning")
	}
	a.PublishedAt, err = alertTime(d.Issued)
	if err != nil {
		return a, err
	}
	if d.Expires != "" {
		a.ExpiresAt, err = alertTime(d.Expires)
		if err != nil {
			return a, err
		}
	}
	a.Identifier = d.Identifier
	a.References = d.References
	a.Hazard = d.Hazard
	a.Level = d.Level
	a.Body = d.Body
	a.Cancel = strings.EqualFold(d.MsgType, "Cancel") || explicitAlertCancel(d.Head, d.Body)
	a.Source = "weathercn"
	a.URL = "https://www.weather.com.cn/alarm/newalarmcontent.shtml?file=" + file
	a.Area = alertArea(d.Station, d.Body)
	if a.Area == "" {
		a.Area = alertArea(d.Head, d.Body)
	}
	return canonicalAlert(a, d.Head, s.now())
}

// Explicit source expiry is authoritative only for that publication; a bare
// empty list never manufactures an early cancellation for another source.
func alertExpired(a OfficialAlert, now time.Time) bool {
	return !a.ExpiresAt.IsZero() && !now.Before(a.ExpiresAt)
}
