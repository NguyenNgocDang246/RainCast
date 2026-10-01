package geocode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// DefaultPhotonURL is Komoot's public Photon instance: OSM data like
// Nominatim, but built for search-as-you-type and tolerant of partial and
// unaccented input ("ben tha" finds "Bến Thành"). It is free but often slow
// (seconds), so it backs up LocationIQ.
const DefaultPhotonURL = "https://photon.komoot.io"

// minSuggestRunes is the shortest query worth suggesting for.
const minSuggestRunes = 2

// LatLon is a point used to rank nearby results first.
type LatLon struct{ Lat, Lon float64 }

// Suggest returns places matching a partial query, nearest to bias first.
// Coordinates and links return nothing: they are resolved on submit.
func (c *Client) Suggest(ctx context.Context, q string, bias *LatLon) ([]Place, error) {
	q = strings.Join(strings.Fields(q), " ")
	if utf8.RuneCountInString(q) < minSuggestRunes || urlRe.MatchString(q) {
		return []Place{}, nil
	}
	if _, _, ok := ParseCoords(q); ok {
		return []Place{}, nil
	}
	return c.textSearch(ctx, q, bias, c.SuggestLimit)
}

// nominatimFirst is how many Nominatim results lead a submitted search.
const nominatimFirst = 2

// searchText finds places for a submitted query. Nominatim and the
// autocomplete providers run in parallel: Nominatim is a full geocoder and
// finds areas and structured addresses the autocomplete indexes skip (e.g.
// former districts kept only as historic boundaries), while LocationIQ and
// Photon are better at partial names. The top Nominatim hits lead, then the
// autocomplete results, then the rest.
func (c *Client) searchText(ctx context.Context, q string) ([]Place, error) {
	var (
		wg         sync.WaitGroup
		nom, auto  []Place
		nErr, aErr error
	)
	wg.Add(2)
	go func() { defer wg.Done(); nom, nErr = c.Search(ctx, q) }()
	go func() { defer wg.Done(); auto, aErr = c.textSearch(ctx, q, nil, c.Limit) }()
	wg.Wait()
	if nErr != nil && aErr != nil {
		return nil, aErr
	}

	split := min(nominatimFirst, len(nom))
	merged := []Place{}
	for _, group := range [][]Place{nom[:split], auto, nom[split:]} {
		for _, p := range group {
			if len(merged) < c.Limit && !duplicate(merged, p) {
				merged = append(merged, p)
			}
		}
	}
	return merged, nil
}

// textSearch serves from cache, else LocationIQ while it has quota, else
// Photon.
func (c *Client) textSearch(ctx context.Context, q string, bias *LatLon, limit int) ([]Place, error) {
	if bias == nil {
		bias = c.Bias
	}
	key := "s:" + strings.ToLower(q)
	if bias != nil {
		// Round the bias so nearby users share cache entries.
		key += fmt.Sprintf("@%.1f,%.1f", bias.Lat, bias.Lon)
	}
	key += "#" + strconv.Itoa(limit)
	if p, ok := c.lookup(key); ok {
		return p, nil
	}

	if c.LocationIQKey != "" {
		places, err := c.locationIQ(ctx, q, bias, limit)
		c.stats.locationIQ(err)
		if err == nil {
			c.store(key, places)
			return places, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c.logf("locationiq unavailable, using photon", "err", err)
	}
	places, err := c.photon(ctx, q, bias, limit)
	count(err, &c.stats.photonOK, &c.stats.photonErrors)
	if err != nil {
		return nil, err
	}
	c.store(key, places)
	return places, nil
}

func (c *Client) photon(ctx context.Context, q string, bias *LatLon, limit int) ([]Place, error) {
	if err := c.photonRate.wait(ctx); err != nil {
		return nil, err
	}

	v := url.Values{}
	v.Set("q", q)
	// Over-fetch: results outside Countries and duplicates are dropped below.
	v.Set("limit", strconv.Itoa(limit*2))
	if bias != nil {
		v.Set("lat", strconv.FormatFloat(bias.Lat, 'f', 4, 64))
		v.Set("lon", strconv.FormatFloat(bias.Lon, 'f', 4, 64))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.PhotonURL+"/api/?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("photon: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("photon: http %d", resp.StatusCode)
	}
	var fc photonCollection
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&fc); err != nil {
		return nil, fmt.Errorf("photon: %w", err)
	}
	return c.photonPlaces(fc, limit), nil
}

type photonCollection struct {
	Features []struct {
		Geometry struct {
			Coordinates []float64 `json:"coordinates"` // lon, lat
		} `json:"geometry"`
		Properties struct {
			Name        string `json:"name"`
			HouseNumber string `json:"housenumber"`
			Street      string `json:"street"`
			Locality    string `json:"locality"`
			District    string `json:"district"`
			City        string `json:"city"`
			County      string `json:"county"`
			State       string `json:"state"`
			CountryCode string `json:"countrycode"`
		} `json:"properties"`
	} `json:"features"`
}

// photonPlaces filters by country, builds readable addresses and drops
// near-duplicates (a mall and its bus stop share a name a few meters apart).
func (c *Client) photonPlaces(fc photonCollection, limit int) []Place {
	allowed := map[string]bool{}
	for _, cc := range strings.Split(c.Countries, ",") {
		if cc = strings.TrimSpace(strings.ToUpper(cc)); cc != "" {
			allowed[cc] = true
		}
	}
	places := []Place{}
	for _, f := range fc.Features {
		p := f.Properties
		if len(allowed) > 0 && !allowed[strings.ToUpper(p.CountryCode)] {
			continue
		}
		if len(f.Geometry.Coordinates) != 2 {
			continue
		}
		street := strings.TrimSpace(p.HouseNumber + " " + p.Street)
		name := p.Name
		if name == "" {
			name = street
		}
		if name == "" {
			continue
		}
		pl := Place{
			Name:    name,
			Address: joinUnique(name, street, p.Locality, p.District, p.City, p.County, p.State),
			Lat:     f.Geometry.Coordinates[1],
			Lon:     f.Geometry.Coordinates[0],
		}
		if !duplicate(places, pl) {
			places = append(places, pl)
		}
		if len(places) == limit {
			break
		}
	}
	return places
}

// joinUnique joins the non-empty parts that differ from name and each other.
func joinUnique(name string, parts ...string) string {
	seen := map[string]bool{strings.ToLower(name): true}
	var out []string
	for _, s := range parts {
		k := strings.ToLower(strings.TrimSpace(s))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, strings.TrimSpace(s))
	}
	return strings.Join(out, ", ")
}

// duplicate reports whether places has one with the same name within 300 m.
// Names compare without case or spaces ("Megamall" vs "Mega Mall").
func duplicate(places []Place, p Place) bool {
	key := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), " ", "") }
	for _, q := range places {
		if key(q.Name) == key(p.Name) && distanceM(q.Lat, q.Lon, p.Lat, p.Lon) < 300 {
			return true
		}
	}
	return false
}

func distanceM(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}
