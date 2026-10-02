package geocode

import (
	"errors"
	"sync/atomic"
)

// counters tracks Geoapify usage since start.
type counters struct {
	cacheHits                              atomic.Int64
	autocompleteOK, searchOK, reverseOK    atomic.Int64
	rateLimited, errors                    atomic.Int64
	tileCacheHits, tileFetched, tileErrors atomic.Int64
}

// Stats is a snapshot of Geoapify usage since start.
type Stats struct {
	GeoapifyEnabled bool  `json:"geoapify_enabled"`
	CacheHits       int64 `json:"cache_hits"`
	AutocompleteOK  int64 `json:"autocomplete_ok"`
	SearchOK        int64 `json:"search_ok"`
	ReverseOK       int64 `json:"reverse_ok"`
	// RateLimited counts lookups refused by Geoapify (429) or skipped
	// during the cool-down after one.
	RateLimited   int64 `json:"rate_limited"`
	Errors        int64 `json:"errors"`
	TileCacheHits int64 `json:"tile_cache_hits"`
	TileFetched   int64 `json:"tile_fetched"`
	TileErrors    int64 `json:"tile_errors"`
}

// Stats returns usage counters.
func (c *Client) Stats() Stats {
	s := &c.stats
	return Stats{
		GeoapifyEnabled: c.Key != "",
		CacheHits:       s.cacheHits.Load(),
		AutocompleteOK:  s.autocompleteOK.Load(),
		SearchOK:        s.searchOK.Load(),
		ReverseOK:       s.reverseOK.Load(),
		RateLimited:     s.rateLimited.Load(),
		Errors:          s.errors.Load(),
		TileCacheHits:   s.tileCacheHits.Load(),
		TileFetched:     s.tileFetched.Load(),
		TileErrors:      s.tileErrors.Load(),
	}
}

func (s *counters) count(endpoint string, err error) {
	switch {
	case errors.Is(err, errRateLimited):
		s.rateLimited.Add(1)
	case err != nil:
		s.errors.Add(1)
	case endpoint == autocomplete:
		s.autocompleteOK.Add(1)
	case endpoint == search:
		s.searchOK.Add(1)
	case endpoint == reverse:
		s.reverseOK.Add(1)
	}
}
