package bench

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Rainbow is Rainbow.ai's Nowcast API (https://doc.rainbow.ai): minute by
// minute precipitation for the next 4 hours. The free tier allows about
// 5,000 calls a month.
type Rainbow struct {
	Token string
	Base  string // default https://api.rainbow.ai
	HTTP  *http.Client
}

// NewRainbow returns a client for the API token.
func NewRainbow(token string) *Rainbow {
	return &Rainbow{Token: token, Base: "https://api.rainbow.ai", HTTP: defaultHTTP()}
}

func (r *Rainbow) Name() string { return NameRainbow }

type rainbowResponse struct {
	Forecast []struct {
		Begin int64   `json:"timestampBegin"` // unix s
		End   int64   `json:"timestampEnd"`
		Rate  float64 `json:"precipRate"` // mm/h
	} `json:"forecast"`
}

// Forecast implements Provider.
func (r *Rainbow) Forecast(ctx context.Context, lat, lon float64, now time.Time) ([]Sample, error) {
	var resp rainbowResponse
	// The token goes in a header, so it never shows in a logged URL.
	u := fmt.Sprintf("%s/nowcast/v1/precip/%.4f/%.4f", r.Base, lon, lat)
	h := http.Header{"Ocp-Apim-Subscription-Key": {r.Token}}
	if err := getJSON(ctx, r.HTTP, u, h, &resp); err != nil {
		return nil, err
	}
	pts := make([]point, 0, len(resp.Forecast))
	step := int64(60)
	for _, e := range resp.Forecast {
		pts = append(pts, point{e.Begin, e.Rate})
		if e.End > e.Begin {
			step = e.End - e.Begin
		}
	}
	var out []Sample
	for _, v := range slots(now) {
		if mmh, ok := stepAt(pts, v.Unix(), step, 120); ok {
			out = append(out, Sample{v, mmh, KindMinutely})
		}
	}
	return out, nil
}
