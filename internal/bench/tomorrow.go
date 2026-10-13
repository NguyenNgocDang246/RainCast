package bench

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Tomorrow is Tomorrow.io's weather API (https://docs.tomorrow.io). Its
// minute-by-minute forecast is for paid plans; without it, "now" comes from
// the realtime endpoint and later times are interpolated between hourly
// forecasts (KindHourly), which costs a second call. The free plan allows
// 25 calls an hour and 500 a day.
type Tomorrow struct {
	Key  string
	Base string // default https://api.tomorrow.io
	HTTP *http.Client
}

// NewTomorrow returns a client for the API key.
func NewTomorrow(key string) *Tomorrow {
	return &Tomorrow{Key: key, Base: "https://api.tomorrow.io", HTTP: defaultHTTP()}
}

func (t *Tomorrow) Name() string { return NameTomorrow }

// tomorrowValues holds the precipitation fields, in mm/h with metric units.
// precipitationIntensity is not in every response; the per-type
// intensities add up to it.
type tomorrowValues struct {
	Precipitation *float64 `json:"precipitationIntensity"`
	Rain          *float64 `json:"rainIntensity"`
	FreezingRain  *float64 `json:"freezingRainIntensity"`
	Sleet         *float64 `json:"sleetIntensity"`
	Snow          *float64 `json:"snowIntensity"`
}

func (v tomorrowValues) mmh() (float64, bool) {
	if v.Precipitation != nil {
		return *v.Precipitation, true
	}
	var sum float64
	ok := false
	for _, p := range []*float64{v.Rain, v.FreezingRain, v.Sleet, v.Snow} {
		if p != nil {
			sum += *p
			ok = true
		}
	}
	return sum, ok
}

type tomorrowEntry struct {
	Time   time.Time      `json:"time"`
	Values tomorrowValues `json:"values"`
}

type tomorrowForecast struct {
	Timelines struct {
		Minutely []tomorrowEntry `json:"minutely"`
		Hourly   []tomorrowEntry `json:"hourly"`
	} `json:"timelines"`
}

type tomorrowRealtime struct {
	Data tomorrowEntry `json:"data"`
}

func tomorrowPoints(es []tomorrowEntry) []point {
	out := make([]point, 0, len(es))
	for _, e := range es {
		if v, ok := e.Values.mmh(); ok {
			out = append(out, point{e.Time.Unix(), v})
		}
	}
	return out
}

func (t *Tomorrow) get(ctx context.Context, path string, lat, lon float64, out any) error {
	q := url.Values{
		"location": {fmt.Sprintf("%.4f,%.4f", lat, lon)},
		"units":    {"metric"},
		"apikey":   {t.Key},
	}
	return getJSON(ctx, t.HTTP, t.Base+path+"?"+q.Encode(), nil, out)
}

// Forecast implements Provider.
func (t *Tomorrow) Forecast(ctx context.Context, lat, lon float64, now time.Time) ([]Sample, error) {
	var fc tomorrowForecast
	if err := t.get(ctx, "/v4/weather/forecast", lat, lon, &fc); err != nil {
		return nil, err
	}
	var out []Sample
	if minutely := tomorrowPoints(fc.Timelines.Minutely); len(minutely) > 1 {
		for _, v := range slots(now) {
			if mmh, ok := stepAt(minutely, v.Unix(), 60, 120); ok {
				out = append(out, Sample{v, mmh, KindMinutely})
			}
		}
		return out, nil
	}

	// No minutely forecast on this plan.
	hourly := tomorrowPoints(fc.Timelines.Hourly)
	for _, v := range slots(now)[1:] {
		if mmh, ok := lerpAt(hourly, v.Unix()); ok {
			out = append(out, Sample{v, mmh, KindHourly})
		}
	}
	var rt tomorrowRealtime
	if err := t.get(ctx, "/v4/weather/realtime", lat, lon, &rt); err != nil {
		return out, fmt.Errorf("realtime: %w", err)
	}
	if mmh, ok := rt.Data.Values.mmh(); ok {
		out = append(out, Sample{now, mmh, KindRealtime})
	}
	return out, nil
}
