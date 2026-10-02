package geocode

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ErrUnresolvedLink means a map link carried neither coordinates nor a place name.
var ErrUnresolvedLink = errors.New("geocode: link has no location")

const num = `(-?\d{1,3}(?:\.\d+)?)`

var (
	urlRe    = regexp.MustCompile(`https?://\S+`)
	coordsRe = regexp.MustCompile(`^\s*` + num + `\s*[,;\s]\s*` + num + `\s*$`)
	// !3d<lat>!4d<lon> is the pinned place; @<lat>,<lon> is only the viewport center.
	pinRe = regexp.MustCompile(`!3d` + num + `!4d` + num)
	atRe  = regexp.MustCompile(`@` + num + `,` + num)
)

// Resolve accepts an address, a "lat, lon" pair or a Google Maps link
// (full or maps.app.goo.gl short link) and returns candidate places.
func (c *Client) Resolve(ctx context.Context, input string) ([]Place, error) {
	input = strings.TrimSpace(input)
	if lat, lon, ok := ParseCoords(input); ok {
		// The name is best-effort: the coordinates alone are enough.
		p, err := c.Reverse(ctx, lat, lon)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			c.logf("reverse failed, using coordinates", "err", err)
		}
		return []Place{p}, nil
	}
	// Share sheets often copy "Place name\nhttps://maps.app.goo.gl/…".
	if link := urlRe.FindString(input); link != "" {
		input = link
	}
	u, err := url.Parse(input)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return c.searchText(ctx, input)
	}
	if c.ShortLinkHosts[strings.ToLower(u.Hostname())] {
		if u, err = c.expand(ctx, u); err != nil {
			return nil, err
		}
	}
	p, hasCoords := ParseMapsURL(u)
	switch {
	case hasCoords:
		return []Place{p}, nil
	case p.Name != "":
		return c.searchText(ctx, p.Name)
	default:
		return nil, ErrUnresolvedLink
	}
}

// ParseCoords parses "lat, lon" (comma, semicolon or space separated).
func ParseCoords(s string) (lat, lon float64, ok bool) {
	m := coordsRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	return validPair(m[1], m[2])
}

func validPair(a, b string) (lat, lon float64, ok bool) {
	lat, err1 := strconv.ParseFloat(a, 64)
	lon, err2 := strconv.ParseFloat(b, 64)
	if err1 != nil || err2 != nil || lat < -85 || lat > 85 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
}

// ParseMapsURL extracts a location from a Google Maps URL. It prefers the
// pinned place, then coordinates in the query, then the viewport center.
// When there are no coordinates it may still return a place name to search.
func ParseMapsURL(u *url.URL) (p Place, hasCoords bool) {
	full := u.String()
	if dec, err := url.PathUnescape(full); err == nil {
		full = dec
	}
	p.Name = placeName(u)
	p.Address = p.Name

	try := func(a, b string) bool {
		if lat, lon, ok := validPair(a, b); ok {
			p.Lat, p.Lon = lat, lon
			return true
		}
		return false
	}
	if m := pinRe.FindStringSubmatch(full); m != nil && try(m[1], m[2]) {
		return withDefaultName(p), true
	}
	q := u.Query()
	for _, k := range []string{"q", "query", "ll", "center", "destination", "daddr"} {
		if lat, lon, ok := ParseCoords(q.Get(k)); ok {
			p.Lat, p.Lon = lat, lon
			return withDefaultName(p), true
		}
	}
	if m := atRe.FindStringSubmatch(full); m != nil && try(m[1], m[2]) {
		return withDefaultName(p), true
	}
	return p, false
}

func withDefaultName(p Place) Place {
	if p.Name == "" {
		p.Name = coordsPlace(p.Lat, p.Lon).Name
	}
	return p
}

// placeName reads /maps/place/<name>/... or a textual q= parameter.
func placeName(u *url.URL) string {
	parts := strings.Split(u.EscapedPath(), "/")
	for i, s := range parts {
		if s == "place" && i+1 < len(parts) {
			if name, err := url.PathUnescape(parts[i+1]); err == nil {
				name = strings.TrimSpace(strings.ReplaceAll(name, "+", " "))
				if _, _, isCoords := ParseCoords(name); !isCoords && name != "" {
					return name
				}
			}
		}
	}
	for _, k := range []string{"q", "query"} {
		if v := strings.TrimSpace(u.Query().Get(k)); v != "" {
			if _, _, isCoords := ParseCoords(v); !isCoords {
				return v
			}
		}
	}
	return ""
}

// expand follows a short link's redirects without fetching the final page.
func (c *Client) expand(ctx context.Context, u *url.URL) (*url.URL, error) {
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for range 5 {
		if !c.ShortLinkHosts[strings.ToLower(u.Hostname())] {
			return u, nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.UserAgent)
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("geocode: expand link: %w", err)
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" {
			return nil, fmt.Errorf("geocode: short link did not redirect (http %d)", resp.StatusCode)
		}
		next, err := u.Parse(loc)
		if err != nil {
			return nil, fmt.Errorf("geocode: bad redirect: %w", err)
		}
		u = next
	}
	return nil, errors.New("geocode: too many redirects")
}
