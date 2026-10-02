// Package geoip looks up a client's country in a local MaxMind-format
// database (DB-IP's IP-to-Country Lite), so searches can favor places nearby.
package geoip

import (
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// How often the file is checked: the geoip service replaces it monthly, and may
// write it the first time only after the server started.
const (
	recheck        = time.Hour
	recheckMissing = time.Minute
)

// DB answers country lookups from the file at its path, loading it when it
// appears and reloading it when it changes. A nil DB knows no countries.
type DB struct {
	path string
	log  *slog.Logger // optional

	mu  sync.RWMutex // guards r: lookups read it while a reload swaps it
	r   *maxminddb.Reader
	mod time.Time

	checkMu sync.Mutex
	checked time.Time
}

// Open loads the database at path. When that fails it still returns a DB,
// which knows no countries until the file can be loaded.
func Open(path string, log *slog.Logger) (*DB, error) {
	d := &DB{path: path, log: log}
	return d, d.load(time.Now())
}

// Country returns the lowercase ISO code of ip's country ("vn", "sg"), or
// "" when unknown.
func (d *DB) Country(ip netip.Addr) string {
	if d == nil || !ip.IsValid() {
		return ""
	}
	d.reload(time.Now())
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.r == nil {
		return ""
	}
	var cc string
	if err := d.r.Lookup(ip.Unmap()).DecodePath(&cc, "country", "iso_code"); err != nil {
		return ""
	}
	return strings.ToLower(cc)
}

// Close releases the database.
func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.r == nil {
		return nil
	}
	err := d.r.Close()
	d.r = nil
	return err
}

// reload loads the file when it changed, checking at most once per recheck
// (recheckMissing while nothing is loaded). A failed load keeps the old data.
func (d *DB) reload(now time.Time) {
	d.mu.RLock()
	every := recheck
	if d.r == nil {
		every = recheckMissing
	}
	d.mu.RUnlock()
	d.checkMu.Lock()
	due := now.Sub(d.checked) >= every
	d.checkMu.Unlock()
	if !due {
		return
	}
	if err := d.load(now); err != nil && !os.IsNotExist(err) && d.log != nil {
		d.log.Warn("geoip reload", "err", err)
	}
}

func (d *DB) load(now time.Time) error {
	d.checkMu.Lock()
	defer d.checkMu.Unlock()
	d.checked = now
	st, err := os.Stat(d.path)
	if err != nil {
		return err
	}
	d.mu.RLock()
	same := d.r != nil && st.ModTime().Equal(d.mod)
	d.mu.RUnlock()
	if same {
		return nil
	}
	r, err := maxminddb.Open(d.path)
	if err != nil {
		return err
	}
	d.mu.Lock()
	old := d.r
	d.r, d.mod = r, st.ModTime()
	d.mu.Unlock()
	if old != nil {
		old.Close()
	} else if d.log != nil {
		d.log.Info("geoip loaded", "path", d.path)
	}
	return nil
}
