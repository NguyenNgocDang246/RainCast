package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	// ErrNoKey means no Geoapify key is configured.
	ErrNoKey = errors.New("geocode: no geoapify key")
	// errRateLimited means Geoapify refused the request for quota reasons.
	errRateLimited = errors.New("geoapify: rate limited")
)

// minSuggestRunes is the shortest query worth suggesting for.
const minSuggestRunes = 2

// Geoapify endpoints, also the stats buckets.
const (
	autocomplete = "autocomplete"
	search       = "search"
	reverse      = "reverse"
)

// Suggest returns places matching a partial query, nearest to bias first.
// Coordinates and links return nothing: they are resolved on submit.
func (c *Client) Suggest(ctx context.Context, q string, bias *LatLon) ([]Place, error) {
	q = strings.Join(strings.Fields(q), " ")
	if c.Key == "" || utf8.RuneCountInString(q) < minSuggestRunes || urlRe.MatchString(q) {
		return []Place{}, nil
	}
	if _, _, ok := ParseCoords(q); ok {
		return []Place{}, nil
	}
	return c.textSearch(ctx, autocomplete, q, bias, c.SuggestLimit)
}

// searchText finds places for a submitted query with the full geocoder,
// which also knows areas and structured addresses autocomplete may skip.
func (c *Client) searchText(ctx context.Context, q string) ([]Place, error) {
	q = strings.Join(strings.Fields(q), " ")
	if q == "" {
		return []Place{}, nil
	}
	return c.textSearch(ctx, search, q, nil, c.Limit)
}

func (c *Client) textSearch(ctx context.Context, endpoint, q string, bias *LatLon, limit int) ([]Place, error) {
	if c.Key == "" {
		return nil, ErrNoKey
	}
	if bias == nil {
		bias = c.Bias
	}
	key := endpoint + ":" + strings.ToLower(q)
	if bias != nil {
		// Round the bias so nearby users share cache entries.
		key += fmt.Sprintf("@%.1f,%.1f", bias.Lat, bias.Lon)
	}
	key += "#" + strconv.Itoa(limit)
	if p, ok := c.lookup(key); ok {
		return p, nil
	}

	v := url.Values{}
	v.Set("text", q)
	v.Set("limit", strconv.Itoa(limit))
	if f := c.countryFilter(); f != "" {
		v.Set("filter", f)
	}
	if bias != nil {
		v.Set("bias", fmt.Sprintf("proximity:%.4f,%.4f", bias.Lon, bias.Lat))
	}
	places, err := c.get(ctx, endpoint, v, limit)
	if err != nil {
		return nil, err
	}
	c.store(key, places)
	return places, nil
}

// Reverse names the place at lat, lon. The result keeps the given
// coordinates; on error it is still usable, named by its coordinates.
func (c *Client) Reverse(ctx context.Context, lat, lon float64) (Place, error) {
	p := coordsPlace(lat, lon)
	if c.Key == "" {
		return p, ErrNoKey
	}
	key := fmt.Sprintf("r:%.4f,%.4f", lat, lon)
	found, ok := c.lookup(key)
	if !ok {
		v := url.Values{}
		v.Set("lat", strconv.FormatFloat(lat, 'f', 6, 64))
		v.Set("lon", strconv.FormatFloat(lon, 'f', 6, 64))
		v.Set("limit", "1")
		var err error
		if found, err = c.get(ctx, reverse, v, 1); err != nil {
			return p, err
		}
		c.store(key, found)
	}
	if len(found) > 0 {
		p.Name, p.Address = found[0].Name, found[0].Address
	}
	return p, nil
}

func coordsPlace(lat, lon float64) Place {
	return Place{Name: fmt.Sprintf("%.5f, %.5f", lat, lon), Lat: lat, Lon: lon}
}

// countryFilter turns "vn,la" into Geoapify's "countrycode:vn,la".
func (c *Client) countryFilter() string {
	var ccs []string
	for _, cc := range strings.Split(c.Countries, ",") {
		if cc = strings.ToLower(strings.TrimSpace(cc)); cc != "" {
			ccs = append(ccs, cc)
		}
	}
	if len(ccs) == 0 {
		return ""
	}
	return "countrycode:" + strings.Join(ccs, ",")
}

// get calls a /v1/geocode endpoint and counts the outcome.
func (c *Client) get(ctx context.Context, endpoint string, v url.Values, limit int) ([]Place, error) {
	places, err := c.fetch(ctx, endpoint, v, limit)
	if ctx.Err() != nil {
		return nil, ctx.Err() // the caller gave up (e.g. a newer keystroke)
	}
	c.stats.count(endpoint, err)
	if err != nil {
		c.logf("geoapify failed", "endpoint", endpoint, "err", err)
	}
	return places, err
}

type geoapifyResponse struct {
	Results []struct {
		Name         string  `json:"name"`
		Lat          float64 `json:"lat"`
		Lon          float64 `json:"lon"`
		Formatted    string  `json:"formatted"`
		AddressLine1 string  `json:"address_line1"`
		AddressLine2 string  `json:"address_line2"`
	} `json:"results"`
}

func (c *Client) fetch(ctx context.Context, endpoint string, v url.Values, limit int) ([]Place, error) {
	if c.pause.active(time.Now()) {
		return nil, errRateLimited
	}
	if err := c.rate.wait(ctx); err != nil {
		return nil, err
	}
	v.Set("apiKey", c.Key)
	v.Set("format", "json")
	if c.Language != "" {
		v.Set("lang", c.Language)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/geocode/"+endpoint+"?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geoapify: %w", stripURL(err))
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests:
		c.pause.extend(time.Now(), 2*time.Second)
		return nil, errRateLimited
	default:
		return nil, fmt.Errorf("geoapify: http %d", resp.StatusCode)
	}
	var r geoapifyResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&r); err != nil {
		return nil, fmt.Errorf("geoapify: %w", err)
	}
	places := []Place{}
	for _, res := range r.Results {
		name := res.AddressLine1
		if name == "" {
			name = res.Name
		}
		if name == "" {
			name, _, _ = strings.Cut(res.Formatted, ",")
		}
		if name == "" {
			continue
		}
		p := Place{Name: name, Address: joinUnique(name, res.AddressLine2), Lat: res.Lat, Lon: res.Lon}
		if !duplicate(places, p) {
			places = append(places, p)
		}
		if len(places) == limit {
			break
		}
	}
	return places, nil
}

// stripURL drops the request URL from a transport error: it carries the key.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
