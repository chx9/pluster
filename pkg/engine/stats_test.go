package engine

import (
	"strings"
	"testing"
	"time"
)

func TestStatsConnections(t *testing.T) {
	s := newStats()
	if s.currConnections.Load() != 0 {
		t.Fatal("expected 0 initial connections")
	}
	s.incConnections()
	s.incConnections()
	if got := s.currConnections.Load(); got != 2 {
		t.Fatalf("expected 2 curr, got %d", got)
	}
	if got := s.totalConnections.Load(); got != 2 {
		t.Fatalf("expected 2 total, got %d", got)
	}
	s.decConnections()
	if got := s.currConnections.Load(); got != 1 {
		t.Fatalf("expected 1 after dec, got %d", got)
	}
	if got := s.totalConnections.Load(); got != 2 {
		t.Fatalf("total should stay 2, got %d", got)
	}
}

func TestStatsCommands(t *testing.T) {
	s := newStats()
	for i := 0; i < 100; i++ {
		s.incCommands()
	}
	if got := s.totalCommands.Load(); got != 100 {
		t.Fatalf("expected 100 commands, got %d", got)
	}
}

func TestStatsQPSRollover(t *testing.T) {
	s := newStats()
	s.qpsBucketSec.Store(time.Now().Unix() - 2)
	s.qpsBucketCount.Store(500)
	s.qpsLastValue.Store(500)

	if got := s.qps(); got != 0 {
		t.Fatalf("stale bucket: expected 0 QPS, got %d", got)
	}
}

func TestStatsSnapshotFormat(t *testing.T) {
	s := newStats()
	s.incConnections()
	s.incConnections()
	s.decConnections()
	s.incCommands()
	s.incCommands()

	snap := s.Snapshot()
	if snap.TotalConnections != 2 {
		t.Fatalf("expected 2 total connections, got %d", snap.TotalConnections)
	}
	if snap.CurrConnections != 1 {
		t.Fatalf("expected 1 curr connection, got %d", snap.CurrConnections)
	}
	if snap.TotalCommands != 2 {
		t.Fatalf("expected 2 commands, got %d", snap.TotalCommands)
	}
	if snap.NumCPU <= 0 {
		t.Fatal("expected positive NumCPU")
	}
	if snap.HeapAllocBytes <= 0 {
		t.Fatal("expected positive HeapAllocBytes")
	}

	text := snap.Format()
	for _, field := range []string{
		"uptime_in_seconds",
		"total_connections_received",
		"connected_clients",
		"total_commands_processed",
		"instantaneous_ops_per_sec",
		"heap_alloc_bytes",
		"heap_sys_bytes",
		"num_cpu",
		"num_goroutines",
		"used_cpu_sys_pct",
		"used_cpu_user_pct",
		"total_gc_runs",
		"gc_pause_total_ns",
	} {
		if !strings.Contains(text, field) {
			t.Fatalf("Format() missing field %q", field)
		}
	}
}
