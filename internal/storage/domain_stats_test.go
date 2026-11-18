package storage

import (
	"context"
	"testing"
	"time"
)

func insertOK(t *testing.T, s *Store, ts time.Time, host string, clientID, bytesIn, durMS, connMS int64) {
	t.Helper()
	_, err := s.InsertProxyEvent(context.Background(), ProxyEvent{
		TS: ts, ClientID: clientID, TargetHost: host, Protocol: "http", Outcome: "ok",
		BytesIn: bytesIn, DurationMS: durMS, ConnectMS: connMS,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestDomainStats_PeriodsSurviveEventRetention(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()

	insertOK(t, s, now, "a.com:443", 0, 100, 10, 4)
	insertOK(t, s, now, "a.com:80", 0, 50, 30, 0)
	insertOK(t, s, now.Add(-3*24*time.Hour), "b.com:443", 0, 10, 5, 2)
	insertOK(t, s, now.Add(-20*24*time.Hour), "c.com:443", 0, 1, 1, 1)
	// Failures are not counted.
	if _, err := s.InsertProxyEvent(ctx, ProxyEvent{TS: now, TargetHost: "d.com:443", Outcome: "failed"}); err != nil {
		t.Fatal(err)
	}

	// The 24h log sweep must not affect the aggregate.
	if _, err := s.PurgeProxyEventsOlderThan(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	week := DomainStatsQuery{Since: now.Add(-7 * 24 * time.Hour), Sort: "requests", Desc: true}
	month := DomainStatsQuery{Since: now.Add(-30 * 24 * time.Hour), Sort: "requests", Desc: true}

	wn, err := s.CountDomainStats(ctx, week)
	if err != nil {
		t.Fatal(err)
	}
	mn, err := s.CountDomainStats(ctx, month)
	if err != nil {
		t.Fatal(err)
	}
	if wn != 2 || mn != 3 {
		t.Fatalf("week=%d month=%d, want 2/3", wn, mn)
	}

	got, err := s.DomainStats(ctx, week)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Domain != "a.com" {
		t.Fatalf("unexpected rows: %+v", got)
	}
	a := got[0]
	if a.Requests != 2 || a.BytesIn != 150 || a.AvgDurationMS != 20 || a.AvgConnectMS != 4 {
		t.Fatalf("bad a.com aggregate: %+v", a)
	}
}

func TestDomainStats_SortAndPaginate(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	insertOK(t, s, now, "b.com:443", 0, 300, 1, 0)
	insertOK(t, s, now, "a.com:443", 0, 200, 1, 0)
	insertOK(t, s, now, "c.com:443", 0, 100, 1, 0)

	q := DomainStatsQuery{Since: now.Add(-time.Hour), Sort: "domain", Limit: 2}
	got, err := s.DomainStats(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Domain != "a.com" || got[1].Domain != "b.com" {
		t.Fatalf("page 1: %+v", got)
	}
	q.Offset = 2
	got, _ = s.DomainStats(ctx, q)
	if len(got) != 1 || got[0].Domain != "c.com" {
		t.Fatalf("page 2: %+v", got)
	}

	got, _ = s.DomainStats(ctx, DomainStatsQuery{Since: now.Add(-time.Hour), Sort: "in", Desc: true})
	if got[0].Domain != "b.com" {
		t.Fatalf("sort by bytes in desc: %+v", got)
	}
	// Unknown sort keys fall back instead of reaching SQL.
	if _, err := s.DomainStats(ctx, DomainStatsQuery{Since: now.Add(-time.Hour), Sort: "1; DROP TABLE x"}); err != nil {
		t.Fatalf("unknown sort should fall back: %v", err)
	}
}

func TestDomainStats_FilterByClientAndReset(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	insertOK(t, s, now, "a.com:443", 7, 1, 1, 0)
	insertOK(t, s, now, "b.com:443", 8, 1, 1, 0)

	got, err := s.DomainStats(ctx, DomainStatsQuery{ClientID: 7, Since: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Domain != "a.com" {
		t.Fatalf("client filter: %+v", got)
	}

	if _, err := s.PurgeAllProxyEvents(ctx); err != nil {
		t.Fatal(err)
	}
	n, _ := s.CountDomainStats(ctx, DomainStatsQuery{Since: now.Add(-time.Hour)})
	if n != 0 {
		t.Fatalf("reset should clear the aggregate, %d left", n)
	}
}

func TestListProxyEventsFiltered_Sort(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	insertOK(t, s, now.Add(-2*time.Minute), "b.com:443", 0, 5, 1, 0)
	insertOK(t, s, now.Add(-1*time.Minute), "a.com:443", 0, 9, 1, 0)

	f := ProxyEventFilter{Since: now.Add(-time.Hour), Sort: "target"}
	got, err := s.ListProxyEventsFiltered(ctx, f, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].TargetHost != "a.com:443" {
		t.Fatalf("sort by target asc: %+v", got)
	}
	f = ProxyEventFilter{Since: now.Add(-time.Hour), Sort: "nonsense", Desc: true}
	got, err = s.ListProxyEventsFiltered(ctx, f, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].TargetHost != "a.com:443" {
		t.Fatalf("fallback should be newest first: %+v", got)
	}
}

func TestMigration_BackfillStripsPort(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	// Same expression the migration uses to strip ":port".
	cases := map[string]string{"a.com:443": "a.com", "a.com": "a.com", "10.0.0.1:8080": "10.0.0.1", "host1": "host1"}
	for in, want := range cases {
		var got string
		err := s.db.QueryRow(`SELECT CASE WHEN rtrim(?1, '0123456789') LIKE '%:' AND rtrim(?1, '0123456789') <> ?1
		         THEN substr(?1, 1, length(rtrim(?1, '0123456789')) - 1) ELSE ?1 END`, in).Scan(&got)
		if err != nil {
			t.Fatal(err)
		}
		if got != want || domainOf(in) != want {
			t.Fatalf("%q: sql=%q go=%q want %q", in, got, domainOf(in), want)
		}
	}
}
