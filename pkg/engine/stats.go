package engine

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Stats struct {
	totalConnections atomic.Int64
	currConnections  atomic.Int64
	totalCommands    atomic.Int64

	qpsBucketSec   atomic.Int64
	qpsBucketCount atomic.Int64
	qpsLastValue   atomic.Int64

	startTime time.Time

	cpuMu          sync.Mutex
	lastCPUSample  time.Time
	lastCPUSys     float64
	lastCPUUser    float64
	cpuSysPct      float64
	cpuUserPct     float64
}

func newStats() *Stats {
	return &Stats{startTime: time.Now()}
}

func (s *Stats) incConnections() {
	s.totalConnections.Add(1)
	s.currConnections.Add(1)
}

func (s *Stats) decConnections() {
	s.currConnections.Add(-1)
}

func (s *Stats) incCommands() {
	s.addCommands(1)
}

func (s *Stats) addCommands(n uint64) {
	s.totalCommands.Add(int64(n))

	now := time.Now().Unix()
	bucketSec := s.qpsBucketSec.Load()
	if bucketSec == now {
		s.qpsBucketCount.Add(int64(n))
		return
	}
	if s.qpsBucketSec.CompareAndSwap(bucketSec, now) {
		old := s.qpsBucketCount.Swap(int64(n))
		s.qpsLastValue.Store(old)
	} else {
		s.qpsBucketCount.Add(int64(n))
	}
}

func (s *Stats) qps() int64 {
	now := time.Now().Unix()
	bucketSec := s.qpsBucketSec.Load()
	if bucketSec == 0 {
		return 0
	}
	if now > bucketSec+1 {
		return 0
	}
	return s.qpsLastValue.Load()
}

func (s *Stats) Snapshot() StatsSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	snap := StatsSnapshot{
		UptimeSeconds:    int64(time.Since(s.startTime).Seconds()),
		TotalConnections: s.totalConnections.Load(),
		CurrConnections:  s.currConnections.Load(),
		TotalCommands:    s.totalCommands.Load(),
		QPS:              s.qps(),
		HeapAllocBytes:   int64(ms.HeapAlloc),
		HeapSysBytes:     int64(ms.HeapSys),
		NumGoroutines:    int64(runtime.NumGoroutine()),
		NumCPU:           int64(runtime.NumCPU()),
		NumGC:            int64(ms.NumGC),
		PauseTotalNs:     int64(ms.PauseTotalNs),
	}

	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err == nil {
		curSys := float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6
		curUser := float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6
		snap.UsedCPUSys = curSys
		snap.UsedCPUUser = curUser

		now := time.Now()
		s.cpuMu.Lock()
		if !s.lastCPUSample.IsZero() {
			elapsed := now.Sub(s.lastCPUSample).Seconds()
			if elapsed > 0 {
				s.cpuSysPct = (curSys - s.lastCPUSys) / elapsed * 100
				s.cpuUserPct = (curUser - s.lastCPUUser) / elapsed * 100
			}
		}
		s.lastCPUSample = now
		s.lastCPUSys = curSys
		s.lastCPUUser = curUser
		snap.CPUSysPct = s.cpuSysPct
		snap.CPUUserPct = s.cpuUserPct
		s.cpuMu.Unlock()
	}

	return snap
}

type StatsSnapshot struct {
	UptimeSeconds    int64
	TotalConnections int64
	CurrConnections  int64
	TotalCommands    int64
	QPS              int64
	HeapAllocBytes   int64
	HeapSysBytes     int64
	NumGoroutines    int64
	NumCPU           int64
	NumGC            int64
	PauseTotalNs     int64
	UsedCPUSys       float64
	UsedCPUUser      float64
	CPUSysPct        float64
	CPUUserPct       float64
}

func (snap StatsSnapshot) Format() string {
	return fmt.Sprintf(
		"# Stats\r\n"+
			"uptime_in_seconds:%d\r\n"+
			"total_connections_received:%d\r\n"+
			"connected_clients:%d\r\n"+
			"total_commands_processed:%d\r\n"+
			"instantaneous_ops_per_sec:%d\r\n"+
			"\r\n"+
			"# Memory\r\n"+
			"heap_alloc_bytes:%d\r\n"+
			"heap_sys_bytes:%d\r\n"+
			"\r\n"+
			"# CPU\r\n"+
			"num_cpu:%d\r\n"+
			"num_goroutines:%d\r\n"+
			"used_cpu_sys:%.6f\r\n"+
			"used_cpu_user:%.6f\r\n"+
			"used_cpu_sys_pct:%.2f\r\n"+
			"used_cpu_user_pct:%.2f\r\n"+
			"\r\n"+
			"# GC\r\n"+
			"total_gc_runs:%d\r\n"+
			"gc_pause_total_ns:%d\r\n",
		snap.UptimeSeconds,
		snap.TotalConnections,
		snap.CurrConnections,
		snap.TotalCommands,
		snap.QPS,
		snap.HeapAllocBytes,
		snap.HeapSysBytes,
		snap.NumCPU,
		snap.NumGoroutines,
		snap.UsedCPUSys,
		snap.UsedCPUUser,
		snap.CPUSysPct,
		snap.CPUUserPct,
		snap.NumGC,
		snap.PauseTotalNs,
	)
}
