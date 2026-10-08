package pipeline

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"raincast/internal/cache"
)

// tileStore keeps radar tiles between forecasts, so a client uploads only
// the tiles the server has not seen (ForecastFromTiles) and the server
// downloads only those it has not (ServerForecast). Tiles the server
// downloaded are kept by TileID. Uploaded tiles are kept only by the SHA-256
// of their bytes: a client reaches one only by naming the hash of a tile it
// downloaded itself, so a forged upload never stands in for a genuine tile.
type tileStore struct {
	shared cache.Shared

	mu    sync.Mutex
	size  int
	order *list.List               // of *storedTile, most recently used first
	byKey map[string]*list.Element // by storedTile.key
}

type storedTile struct {
	key  string
	data []byte
}

const (
	// tileStoreBytes bounds the tiles kept in memory (~12 KB each).
	tileStoreBytes = 32 << 20
	// sharedTileTTL outlasts the frames a forecast reads (pairs+1 frames,
	// 10 minutes apart).
	sharedTileTTL = 90 * time.Minute
	// sharedTilePrefix keys tiles in the shared cache.
	sharedTilePrefix = "rc:tile:"
)

func newTileStore(shared cache.Shared) *tileStore {
	return &tileStore{shared: shared, order: list.New(), byKey: map[string]*list.Element{}}
}

// tileSum is the hex SHA-256 of a tile, as ContentHash and clients compute it.
func tileSum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// validSum reports whether s looks like a tileSum.
func validSum(s string) bool {
	if len(s) != 2*sha256.Size {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func idKey(id TileID) string   { return fmt.Sprintf("id:%d:%d:%d", id.Time, id.X, id.Y) }
func sumKey(sum string) string { return "sum:" + sum }
func claimKey(id TileID) string {
	return fmt.Sprintf("claim:%d:%d:%d", id.Time, id.X, id.Y)
}

// trusted returns a tile the server downloaded from RainViewer.
func (s *tileStore) trusted(ctx context.Context, id TileID) ([]byte, bool) {
	return s.get(ctx, idKey(id))
}

func (s *tileStore) putTrusted(ctx context.Context, id TileID, data []byte) {
	s.put(ctx, idKey(id), data)
}

// putSum keeps a tile under its hash only.
func (s *tileStore) putSum(ctx context.Context, data []byte) {
	s.put(ctx, sumKey(tileSum(data)), data)
}

// claim notes that an upload of id hashed to sum, so plans can offer it
// (knownSum). A claim is only a hint: the client compares it with the hash
// of the tile it downloaded, and a wrong one only costs it an upload.
func (s *tileStore) claim(ctx context.Context, id TileID, sum string) {
	s.put(ctx, claimKey(id), []byte(sum))
}

// knownSum is the hash of a tile id the server has: its own download's,
// else the last upload claimed.
func (s *tileStore) knownSum(ctx context.Context, id TileID) (string, bool) {
	if data, ok := s.trusted(ctx, id); ok {
		return tileSum(data), true
	}
	if sum, ok := s.get(ctx, claimKey(id)); ok && validSum(string(sum)) {
		return string(sum), true
	}
	return "", false
}

// lookup returns the tile id whose hash is sum: the server's own download
// when it has that content, otherwise an earlier upload of it.
func (s *tileStore) lookup(ctx context.Context, id TileID, sum string) ([]byte, bool) {
	if !validSum(sum) {
		return nil, false
	}
	if data, ok := s.trusted(ctx, id); ok && tileSum(data) == sum {
		return data, true
	}
	if data, ok := s.get(ctx, sumKey(sum)); ok && tileSum(data) == sum {
		return data, true
	}
	return nil, false
}

// get finds key in memory, then in the shared cache.
func (s *tileStore) get(ctx context.Context, key string) ([]byte, bool) {
	s.mu.Lock()
	if e, ok := s.byKey[key]; ok {
		s.order.MoveToFront(e)
		data := e.Value.(*storedTile).data
		s.mu.Unlock()
		return data, true
	}
	s.mu.Unlock()
	if s.shared == nil {
		return nil, false
	}
	data, ok := s.shared.Get(ctx, sharedTilePrefix+key)
	if !ok {
		return nil, false
	}
	s.remember(key, data)
	return data, true
}

func (s *tileStore) put(ctx context.Context, key string, data []byte) {
	s.remember(key, data)
	if s.shared != nil {
		s.shared.Set(ctx, sharedTilePrefix+key, data, sharedTileTTL)
	}
}

// remember keeps key in memory, dropping the least recently used beyond
// tileStoreBytes.
func (s *tileStore) remember(key string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.byKey[key]; ok {
		s.order.MoveToFront(e)
		return
	}
	s.byKey[key] = s.order.PushFront(&storedTile{key, data})
	s.size += len(data)
	for s.size > tileStoreBytes {
		e := s.order.Back()
		t := e.Value.(*storedTile)
		s.order.Remove(e)
		delete(s.byKey, t.key)
		s.size -= len(t.data)
	}
}
