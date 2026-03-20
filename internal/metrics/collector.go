package metrics

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/net"
)

// Snapshot holds a point-in-time system metrics reading.
type Snapshot struct {
	CPUPercent     float64
	MemoryMB       int
	MemorySysMB    int
	NetworkTxBytes uint64
	NetworkRxBytes uint64
	Goroutines     int
}

// Collector samples system metrics at a fixed interval.
type Collector struct {
	mu       sync.RWMutex
	latest   Snapshot
	interval time.Duration
	stop     chan struct{}

	// Network baseline (cumulative at start)
	netBaseTx uint64
	netBaseRx uint64
}

// New creates a collector that samples every interval.
func New(interval time.Duration) *Collector {
	tx, rx := netCounters()
	c := &Collector{
		interval:  interval,
		stop:      make(chan struct{}),
		netBaseTx: tx,
		netBaseRx: rx,
	}
	c.sample() // initial reading
	return c
}

// Start begins background sampling. Call Stop() to end.
func (c *Collector) Start() {
	go func() {
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.sample()
			case <-c.stop:
				return
			}
		}
	}()
}

// Stop ends the background sampler.
func (c *Collector) Stop() {
	select {
	case c.stop <- struct{}{}:
	default:
	}
}

// Latest returns the most recent metrics snapshot.
func (c *Collector) Latest() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}

func (c *Collector) sample() {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	cpuPct := 0.0
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		cpuPct = pcts[0]
	}

	tx, rx := netCounters()

	c.mu.Lock()
	c.latest = Snapshot{
		CPUPercent:     cpuPct,
		MemoryMB:       int(mem.Alloc / 1024 / 1024),
		MemorySysMB:    int(mem.Sys / 1024 / 1024),
		NetworkTxBytes: tx - c.netBaseTx,
		NetworkRxBytes: rx - c.netBaseRx,
		Goroutines:     runtime.NumGoroutine(),
	}
	c.mu.Unlock()
}

// netCounters reads cumulative TX/RX bytes from all network interfaces.
func netCounters() (tx, rx uint64) {
	counters, err := net.IOCountersWithContext(context.Background(), false)
	if err != nil || len(counters) == 0 {
		return 0, 0
	}
	return counters[0].BytesSent, counters[0].BytesRecv
}
