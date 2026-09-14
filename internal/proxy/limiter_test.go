package proxy

import (
	"testing"
	"time"
)

func TestLimiterEnforcesRPM(t *testing.T) {
	now := time.Now()
	l := newLimiter()
	l.now = func() time.Time { return now }

	if ok, _ := l.allow("k", 2); !ok {
		t.Fatal("first request should pass")
	}
	if ok, _ := l.allow("k", 2); !ok {
		t.Fatal("second request should pass")
	}
	ok, retry := l.allow("k", 2)
	if ok {
		t.Fatal("third request should be limited")
	}
	if retry <= 0 {
		t.Fatalf("expected positive retry-after, got %v", retry)
	}

	now = now.Add(30 * time.Second)
	if ok, _ := l.allow("k", 2); !ok {
		t.Fatal("request should pass after refill")
	}
}

func TestLimiterDisabled(t *testing.T) {
	l := newLimiter()
	for i := 0; i < 100; i++ {
		if ok, _ := l.allow("k", 0); !ok {
			t.Fatal("rpm <= 0 must disable limiting")
		}
	}
}
