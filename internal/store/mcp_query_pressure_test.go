package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

var pressureFoldOnce sync.Once
var pressureFoldErr error
var pressureFoldStarted chan struct{}
var pressureFoldSignal sync.Once

type pressureFoldQuerier struct{ querier }

func (q pressureFoldQuerier) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return q.querier.QueryRowContext(ctx, strings.ReplaceAll(query, "mcp_fold(", "pressure_fold("), args...)
}

func TestMCPContactQueryPressure(t *testing.T) {
	if os.Getenv("WA_QUERY_PRESSURE") != "1" {
		t.Skip("set WA_QUERY_PRESSURE=1 for the synthetic ceiling measurement")
	}
	pressureFoldOnce.Do(func() {
		pressureFoldErr = sqlite.RegisterDeterministicScalarFunction("pressure_fold", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			pressureFoldSignal.Do(func() { close(pressureFoldStarted) })
			switch v := args[0].(type) {
			case string:
				return fold(v), nil
			case []byte:
				return fold(string(v)), nil
			default:
				return v, nil
			}
		})
	})
	if pressureFoldErr != nil {
		t.Fatal(pressureFoldErr)
	}
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{free: func(string) (uint64, error) { return 2 * gib, nil }}})
	dev := seedDevice(t, st)
	ctx := context.Background()
	// 94,304 names of 42 bytes and 5,696 of 41 bytes: exactly 4 MiB in each field.
	_, err := st.writer.ExecContext(ctx, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x < 100000)
		INSERT INTO whatsmeow_contacts(our_jid,their_jid,full_name,first_name,push_name,business_name)
		SELECT ?, printf('%012d@s.whatsapp.net',x), substr(?,1,CASE WHEN x<=94304 THEN 42 ELSE 41 END),
		substr(?,1,CASE WHEN x<=94304 THEN 42 ELSE 41 END), substr(?,1,CASE WHEN x<=94304 THEN 42 ELSE 41 END),
		substr(?,1,CASE WHEN x<=94304 THEN 42 ELSE 41 END) FROM n`, dev.ID.String(), strings.Repeat("a", 42), strings.Repeat("b", 42), strings.Repeat("c", 42), strings.Repeat("d", 42))
	if err != nil {
		t.Fatal(err)
	}
	var n, chosen, total int64
	if err := st.writer.QueryRowContext(ctx, `SELECT count(*),sum(octet_length(full_name)),sum(octet_length(full_name)+octet_length(first_name)+octet_length(push_name)+octet_length(business_name)) FROM whatsmeow_contacts`).Scan(&n, &chosen, &total); err != nil {
		t.Fatal(err)
	}
	if n != 100000 || chosen != 4*mib || total != 16*mib {
		t.Fatalf("fixture: entries=%d chosen=%d all=%d", n, chosen, total)
	}
	type gauges struct{ cache, spill, temp int }
	measure := func(c *sql.Conn) (g gauges, err error) {
		if err = c.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&g.temp); err != nil {
			return
		}
		err = c.Raw(func(dc any) error {
			s, ok := dc.(sqlite.DBStatus)
			if !ok {
				return fmt.Errorf("DBStatus unavailable")
			}
			var err error
			g.cache, _, err = s.Status(sqlite.DBStatusCacheUsed, false)
			if err != nil {
				return err
			}
			g.spill, _, err = s.Status(sqlite.DBStatusTempbufSpill, false)
			return err
		})
		return
	}
	var before, after gauges
	var beforeErr, afterErr error
	st.hooks.onRead = func(c *sql.Conn) func() {
		before, beforeErr = measure(c)
		return func() { after, afterErr = measure(c) }
	}
	st.hooks.wrapReader = func(q querier) querier { return pressureFoldQuerier{q} }
	pressureFoldStarted = make(chan struct{})
	pressureFoldSignal = sync.Once{}
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	sqliteBefore := memoryUsed()
	memoryHighwater(true)
	var peak atomic.Uint64
	peak.Store(baseline.HeapAlloc)
	stopSample, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stopSample:
				return
			case <-tick.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak.Load() {
					peak.Store(m.HeapAlloc)
				}
			}
		}
	}()
	type outcome struct {
		rows    int
		err     error
		elapsed time.Duration
	}
	queryDone := make(chan outcome, 1)
	go func() {
		start := time.Now()
		rows, err := st.ToolReader().SearchContacts(ctx, "d", Page{Limit: 200})
		queryDone <- outcome{len(rows), err, time.Since(start)}
	}()
	select {
	case <-pressureFoldStarted:
	case r := <-queryDone:
		close(stopSample)
		<-sampled
		t.Fatalf("query ended before folding: %+v", r)
	case <-time.After(15 * time.Second):
		close(stopSample)
		<-sampled
		t.Fatal("query did not begin folding")
	}
	var writes int
	var writeErr error
	var result outcome
loop:
	for {
		select {
		case result = <-queryDone:
			break loop
		default:
		}
		m := msg(phoneJID("15550000000"), fmt.Sprintf("pressure-%d", writes), phoneJID("15550000000"), time.Now())
		m.Text = strings.Repeat("x", 4096)
		if writeErr = st.StoreMessage(ctx, m); writeErr != nil {
			break
		}
		writes++
	}
	if writeErr != nil {
		result = <-queryDone
	}
	close(stopSample)
	<-sampled
	sqlitePeak := memoryHighwater(false)
	if beforeErr != nil || afterErr != nil || before.temp != 2 || after.temp != 2 || before.spill != after.spill {
		t.Fatalf("query connection counters: before=%+v (%v), after=%+v (%v)", before, beforeErr, after, afterErr)
	}
	t.Logf("fixture entries=%d chosen=%d all=%d; query rows=%d elapsed=%s err=%v; concurrent committed writes=%d err=%v", n, chosen, total, result.rows, result.elapsed, result.err, writes, writeErr)
	t.Logf("SQLite process baseline=%d highwater=%d delta=%d limit=%d; sampled Go heap baseline=%d peak=%d delta=%d", sqliteBefore, sqlitePeak, sqlitePeak-sqliteBefore, maxHeapBytes, baseline.HeapAlloc, peak.Load(), int64(peak.Load())-int64(baseline.HeapAlloc))
	t.Logf("same reader cache before=%d after=%d bytes; temp_store before=%d after=%d; temp spill counter before=%d after=%d bytes", before.cache, after.cache, before.temp, after.temp, before.spill, after.spill)
	if result.err != nil || result.rows != 200 || writeErr != nil || writes == 0 {
		t.Fatal("pressure workload did not complete successfully with concurrent ingest")
	}
	if sqlitePeak >= maxHeapBytes {
		t.Fatal("SQLite reached its process heap limit")
	}
}
