package geocode

import (
	"errors"
	"sync/atomic"
)

// counters tracks which provider served each lookup since start.
type counters struct {
	cacheHits                    atomic.Int64
	liqOK, liqLimited, liqErrors atomic.Int64
	photonOK, photonErrors       atomic.Int64
	nominatimOK, nominatimErrors atomic.Int64
}

// Stats is a snapshot of provider usage since start.
type Stats struct {
	LocationIQEnabled bool  `json:"locationiq_enabled"`
	CacheHits         int64 `json:"cache_hits"`
	LocationIQOK      int64 `json:"locationiq_ok"`
	// LocationIQLimited counts requests sent to Photon instead because the
	// local quota was used up or LocationIQ answered 429.
	LocationIQLimited int64 `json:"locationiq_rate_limited"`
	LocationIQErrors  int64 `json:"locationiq_errors"`
	PhotonOK          int64 `json:"photon_ok"`
	PhotonErrors      int64 `json:"photon_errors"`
	NominatimOK       int64 `json:"nominatim_ok"`
	NominatimErrors   int64 `json:"nominatim_errors"`
}

// Stats returns provider usage counters.
func (c *Client) Stats() Stats {
	s := &c.stats
	return Stats{
		LocationIQEnabled: c.LocationIQKey != "",
		CacheHits:         s.cacheHits.Load(),
		LocationIQOK:      s.liqOK.Load(),
		LocationIQLimited: s.liqLimited.Load(),
		LocationIQErrors:  s.liqErrors.Load(),
		PhotonOK:          s.photonOK.Load(),
		PhotonErrors:      s.photonErrors.Load(),
		NominatimOK:       s.nominatimOK.Load(),
		NominatimErrors:   s.nominatimErrors.Load(),
	}
}

func count(err error, ok, failed *atomic.Int64) {
	if err == nil {
		ok.Add(1)
	} else {
		failed.Add(1)
	}
}

func (s *counters) locationIQ(err error) {
	switch {
	case err == nil:
		s.liqOK.Add(1)
	case errors.Is(err, errRateLimited):
		s.liqLimited.Add(1)
	default:
		s.liqErrors.Add(1)
	}
}
