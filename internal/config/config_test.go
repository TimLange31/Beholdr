package config

import "testing"

func TestLoadDefaultsAreSecure(t *testing.T) {
	t.Setenv("BEHOLDR_CORS_ORIGINS", "")
	cfg := Load()
	if len(cfg.CORSOrigins) != 0 {
		t.Fatalf("CORS must be disabled by default, got origins=%v", cfg.CORSOrigins)
	}
	if cfg.Addr != ":8000" {
		t.Errorf("want default addr :8000, got %q", cfg.Addr)
	}
}

func TestLoadCORSOriginsParsesTrimmedCSV(t *testing.T) {
	t.Setenv("BEHOLDR_CORS_ORIGINS", "https://a.example.com, https://b.example.com ,")
	cfg := Load()
	want := []string{"https://a.example.com", "https://b.example.com"}
	if len(cfg.CORSOrigins) != len(want) {
		t.Fatalf("want %v, got %v", want, cfg.CORSOrigins)
	}
	for i := range want {
		if cfg.CORSOrigins[i] != want[i] {
			t.Errorf("origin %d: want %q, got %q", i, want[i], cfg.CORSOrigins[i])
		}
	}
}

func TestEnvIntFallsBackOnInvalidValue(t *testing.T) {
	t.Setenv("BEHOLDR_POLL_INTERVAL", "not-a-number")
	cfg := Load()
	if cfg.PollInterval.Seconds() != 15 {
		t.Errorf("want default poll interval on invalid input, got %v", cfg.PollInterval)
	}
}
