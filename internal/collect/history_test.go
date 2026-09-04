package collect

import "testing"

func TestHistoryPushBoundsToMax(t *testing.T) {
	h := NewHistory(3)
	for i := 0; i < 5; i++ {
		h.Push("s", Point{"t": float64(i)})
	}
	got := h.Get("s")
	if len(got) != 3 {
		t.Fatalf("want 3 points retained, got %d", len(got))
	}
	// ring buffer keeps the most recent N: t=2,3,4
	for i, p := range got {
		want := float64(i + 2)
		if p["t"] != want {
			t.Errorf("point %d: want t=%v, got %v", i, want, p["t"])
		}
	}
}

func TestHistoryGetUnknownSeriesIsEmptyNotNil(t *testing.T) {
	h := NewHistory(3)
	got := h.Get("missing")
	if got == nil {
		t.Fatal("want a non-nil empty slice for an unknown series")
	}
	if len(got) != 0 {
		t.Fatalf("want 0 points, got %d", len(got))
	}
}

func TestHistoryPruneDropsOnlyUnkeptNodeAndMSSeries(t *testing.T) {
	h := NewHistory(10)
	h.Push("cluster", Point{"t": 1})
	h.Push("node::a", Point{"t": 1})
	h.Push("node::b", Point{"t": 1})
	h.Push("ms::default/web", Point{"t": 1})

	h.Prune(map[string]struct{}{"cluster": {}, "node::a": {}})

	if len(h.Get("cluster")) == 0 {
		t.Error("cluster series should never be pruned")
	}
	if len(h.Get("node::a")) == 0 {
		t.Error("node::a is in keep, should survive Prune")
	}
	if len(h.Get("node::b")) != 0 {
		t.Error("node::b is not in keep, should be dropped by Prune")
	}
	if len(h.Get("ms::default/web")) != 0 {
		t.Error("ms::default/web is not in keep, should be dropped by Prune")
	}
}
