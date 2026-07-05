package collect

import (
	"strings"
	"sync"
)

// Point is a single timestamped sample: {"t": <unix>, "<metric>": <value>, ...}.
type Point map[string]float64

// History keeps bounded ring buffers of Points keyed by series id, safe for
// concurrent use.
type History struct {
	mu   sync.RWMutex
	max  int
	data map[string][]Point
}

func NewHistory(max int) *History {
	if max < 1 {
		max = 1
	}
	return &History{max: max, data: map[string][]Point{}}
}

func (h *History) Push(series string, p Point) {
	h.mu.Lock()
	defer h.mu.Unlock()
	buf := h.data[series]
	buf = append(buf, p)
	if len(buf) > h.max {
		buf = buf[len(buf)-h.max:]
	}
	h.data[series] = buf
}

func (h *History) Get(series string) []Point {
	h.mu.RLock()
	defer h.mu.RUnlock()
	src := h.data[series]
	out := make([]Point, len(src))
	copy(out, src)
	return out
}

// Prune drops node/microservice series no longer present in keep.
func (h *History) Prune(keep map[string]struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k := range h.data {
		if _, ok := keep[k]; ok {
			continue
		}
		if strings.HasPrefix(k, "node::") || strings.HasPrefix(k, "ms::") {
			delete(h.data, k)
		}
	}
}
