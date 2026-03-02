package infra

import (
	"context"
	"net"
	"sort"
	"time"
)

// Discover probes all registered hosts for reachability.
// Individual host failures do not abort the scan. MaxConcurrentHosts
// controls how many hosts are probed per cycle; remaining hosts are
// skipped and left stale for the next interval. Stale hosts are
// prioritized on subsequent scans.
func (m *Manager) Discover(ctx context.Context) error {
	m.mu.Lock()
	hostIDs := m.prioritizedHostIDs()
	limit := m.MaxConcurrentHosts
	if limit <= 0 {
		limit = len(hostIDs)
	}
	m.mu.Unlock()

	// Scan up to limit hosts, prioritizing stale ones.
	scanned := 0
	for _, id := range hostIDs {
		if scanned >= limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		m.mu.RLock()
		host, ok := m.hosts[id]
		m.mu.RUnlock()
		if !ok {
			continue
		}

		reachable, checkErr := checkHost(ctx, host)

		m.mu.Lock()
		r := m.reachability[id]
		if r == nil {
			r = &HostReachability{}
			m.reachability[id] = r
		}
		r.LastChecked = time.Now()
		r.Reachable = reachable
		if reachable {
			r.LastSuccess = time.Now()
			r.ErrorCount = 0
			r.LastError = ""
		} else {
			r.ErrorCount++
			if checkErr != nil {
				r.LastError = checkErr.Error()
			}
		}
		m.lastUpdated = time.Now()
		m.mu.Unlock()

		scanned++
	}

	return nil
}

// prioritizedHostIDs returns host IDs sorted with stale hosts first.
// Must be called with m.mu held.
func (m *Manager) prioritizedHostIDs() []string {
	ids := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool {
		ri := m.reachability[ids[i]]
		rj := m.reachability[ids[j]]

		// Never-checked hosts come first.
		iNever := ri == nil || ri.LastChecked.IsZero()
		jNever := rj == nil || rj.LastChecked.IsZero()

		if iNever && !jNever {
			return true
		}
		if !iNever && jNever {
			return false
		}
		if iNever && jNever {
			return ids[i] < ids[j]
		}

		// Then sort by LastChecked ascending (oldest first = most stale).
		return ri.LastChecked.Before(rj.LastChecked)
	})

	return ids
}

// checkHost probes a host's reachability. For hosts with an explicit port
// in the address, it uses TCP connect. Otherwise it attempts a UDP dial
// to the address which succeeds for any routable address without requiring
// a listening service.
func checkHost(ctx context.Context, host Host) (bool, error) {
	addr := host.Address
	if addr == "" {
		return false, nil
	}

	// If the address includes an explicit port, do a TCP connect probe.
	_, _, err := net.SplitHostPort(addr)
	if err == nil {
		dialer := net.Dialer{Timeout: 2 * time.Second}
		conn, dialErr := dialer.DialContext(ctx, "tcp", addr)
		if dialErr != nil {
			return false, dialErr
		}
		conn.Close()
		return true, nil
	}

	// No port specified — use UDP dial as a reachability check.
	// UDP dial to a routable address succeeds without needing a service.
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, dialErr := dialer.DialContext(ctx, "udp", net.JoinHostPort(addr, "1"))
	if dialErr != nil {
		return false, dialErr
	}
	conn.Close()
	return true, nil
}
