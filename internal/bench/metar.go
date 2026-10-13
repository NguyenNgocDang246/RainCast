package bench

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"raincast/internal/store"
)

// METAR fetches airport weather reports from aviationweather.gov's Data API
// (https://aviationweather.gov/data/api/): the ground truth weatherindex.ai
// scores nowcasts against. It needs no key.
type METAR struct {
	Base string // default https://aviationweather.gov
	HTTP *http.Client
}

// NewMETAR returns a client for aviationweather.gov.
func NewMETAR() *METAR {
	return &METAR{Base: "https://aviationweather.gov", HTTP: defaultHTTP()}
}

// MetarMatch is how far from a scored time the nearest report may be.
const MetarMatch = 10 * time.Minute

// MetarThresholdMMH is weatherindex.ai's rain threshold (20 dBZ ≈ 0.5 mm/h),
// applied to forecasts scored against METAR.
const MetarThresholdMMH = 0.5

var icaoRe = regexp.MustCompile(`^[A-Z]{4}$`)

// IsICAO reports whether a station id is an airport's ICAO code, which
// makes it scored against that airport's METAR.
func IsICAO(id string) bool { return icaoRe.MatchString(id) }

type metarReport struct {
	ICAO    string `json:"icaoId"`
	ObsTime int64  `json:"obsTime"` // unix s
	Wx      string `json:"wxString"`
	Raw     string `json:"rawOb"`
}

// Recent returns the reports of the airports over the last hours.
func (m *METAR) Recent(ctx context.Context, icao []string, hours int) ([]store.BenchMetar, error) {
	q := url.Values{"ids": {strings.Join(icao, ",")}, "format": {"json"}, "hours": {strconv.Itoa(hours)}}
	var reps []metarReport
	if err := getJSON(ctx, m.HTTP, m.Base+"/api/data/metar?"+q.Encode(), nil, &reps); err != nil {
		return nil, err
	}
	out := make([]store.BenchMetar, 0, len(reps))
	for _, r := range reps {
		if r.ICAO == "" || r.ObsTime == 0 {
			continue
		}
		out = append(out, store.BenchMetar{Station: r.ICAO, Obs: r.ObsTime, Rain: MetarRain(r.Wx), Raw: r.Raw})
	}
	return out, nil
}

// precipCodes are the METAR present-weather codes for precipitation.
var precipCodes = map[string]bool{
	"RA": true, "DZ": true, "SN": true, "SG": true, "PL": true,
	"GR": true, "GS": true, "IC": true, "UP": true,
}

// MetarRain reports whether present weather (e.g. "-SHRA BR") has
// precipitation at the airport. Weather in the vicinity (VC…) does not
// count, nor does a thunderstorm or shower descriptor without any.
func MetarRain(wx string) bool {
	for _, tok := range strings.Fields(wx) {
		tok = strings.TrimLeft(tok, "+-")
		if strings.HasPrefix(tok, "VC") || strings.HasPrefix(tok, "RE") {
			continue
		}
		for i := 0; i+2 <= len(tok); i += 2 {
			if precipCodes[tok[i:i+2]] {
				return true
			}
		}
	}
	return false
}
