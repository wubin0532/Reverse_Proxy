package webproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/ids"
	"github.com/go-chi/chi/v5"
)

type discoverySpec struct {
	Targets   []string `json:"targets"`
	Ports     []int    `json:"ports"`
	TimeoutMs int      `json:"timeoutMs"`
}
type discoveredPort struct {
	Address   string `json:"address"`
	LatencyMs int64  `json:"latencyMs"`
}
type discoveryJob struct {
	ID        string           `json:"id"`
	Status    string           `json:"status"`
	Total     int              `json:"total"`
	Completed int              `json:"completed"`
	StartedAt time.Time        `json:"startedAt"`
	Results   []discoveredPort `json:"results"`
	cancel    context.CancelFunc
}
type discoveryRuntime struct {
	mu      sync.Mutex
	jobs    map[string]*discoveryJob
	stopped bool
	wg      sync.WaitGroup
}

var discoverySlots = make(chan struct{}, 16)

func discoveryTargets(spec *discoverySpec) ([]netip.Addr, error) {
	if len(spec.Targets) < 1 || len(spec.Targets) > 32 || len(spec.Ports) < 1 || len(spec.Ports) > 16 {
		return nil, fmt.Errorf("须有1到32个IP/CIDR目标与1到16个端口")
	}
	if spec.TimeoutMs == 0 {
		spec.TimeoutMs = 300
	}
	if spec.TimeoutMs < 50 || spec.TimeoutMs > 1000 {
		return nil, fmt.Errorf("连接超时须为50到1000毫秒")
	}
	ports := map[int]bool{}
	for _, p := range spec.Ports {
		if p < 1 || p > 65535 || ports[p] {
			return nil, fmt.Errorf("端口无效或重复")
		}
		ports[p] = true
	}
	addresses := map[netip.Addr]bool{}
	add := func(ip netip.Addr) error {
		ip = ip.Unmap()
		if !ip.IsPrivate() && !ip.IsLoopback() {
			return fmt.Errorf("服务发现仅支持内网或回环地址")
		}
		addresses[ip] = true
		if len(addresses) > 256 {
			return fmt.Errorf("每次最多256个地址")
		}
		return nil
	}
	for _, target := range spec.Targets {
		target = strings.TrimSpace(target)
		if ip, e := netip.ParseAddr(target); e == nil {
			if e = add(ip); e != nil {
				return nil, e
			}
			continue
		}
		prefix, e := netip.ParsePrefix(target)
		if e != nil {
			return nil, fmt.Errorf("目标须为IP或CIDR，不接受域名")
		}
		bits := prefix.Addr().BitLen() - prefix.Bits()
		if bits > 8 {
			return nil, fmt.Errorf("单个CIDR最多256个地址")
		}
		prefix = prefix.Masked()
		for ip := prefix.Addr(); prefix.Contains(ip); ip = ip.Next() {
			if e = add(ip); e != nil {
				return nil, e
			}
		}
	}
	result := make([]netip.Addr, 0, len(addresses))
	for ip := range addresses {
		result = append(result, ip)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Less(result[j]) })
	return result, nil
}
func (d *discoveryRuntime) snapshot(id string) (discoveryJob, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	j, ok := d.jobs[id]
	if !ok {
		return discoveryJob{}, false
	}
	out := *j
	out.cancel = nil
	out.Results = append([]discoveredPort{}, j.Results...)
	sort.Slice(out.Results, func(i, j int) bool { return out.Results[i].Address < out.Results[j].Address })
	return out, true
}
func (d *discoveryRuntime) start(spec discoverySpec) (discoveryJob, error) {
	targets, e := discoveryTargets(&spec)
	if e != nil {
		return discoveryJob{}, e
	}
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return discoveryJob{}, fmt.Errorf("服务正在停止")
	}
	if d.jobs == nil {
		d.jobs = map[string]*discoveryJob{}
	}
	running := 0
	for _, j := range d.jobs {
		if j.Status == "running" {
			running++
		}
	}
	if running >= 2 {
		d.mu.Unlock()
		return discoveryJob{}, fmt.Errorf("最多同时运行2个发现任务")
	}
	for len(d.jobs) >= 8 {
		var oldest *discoveryJob
		for _, j := range d.jobs {
			if j.Status != "running" && (oldest == nil || j.StartedAt.Before(oldest.StartedAt)) {
				oldest = j
			}
		}
		if oldest == nil {
			break
		}
		delete(d.jobs, oldest.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	job := &discoveryJob{ID: ids.New(), Status: "running", Total: len(targets) * len(spec.Ports), StartedAt: time.Now().UTC(), Results: []discoveredPort{}, cancel: cancel}
	d.jobs[job.ID] = job
	out := *job
	out.cancel = nil
	d.wg.Add(1)
	d.mu.Unlock()
	go func() {
		defer d.wg.Done()
		defer cancel()
		work := make(chan string)
		var workers sync.WaitGroup
		for range 16 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for address := range work {
					select {
					case discoverySlots <- struct{}{}:
					case <-ctx.Done():
						return
					}
					start := time.Now()
					conn, e := (&net.Dialer{Timeout: time.Duration(spec.TimeoutMs) * time.Millisecond}).DialContext(ctx, "tcp", address)
					if conn != nil {
						conn.Close()
					}
					<-discoverySlots
					d.mu.Lock()
					job.Completed++
					if e == nil {
						job.Results = append(job.Results, discoveredPort{address, time.Since(start).Milliseconds()})
					}
					d.mu.Unlock()
				}
			}()
		}
	feed:
		for _, ip := range targets {
			for _, port := range spec.Ports {
				select {
				case work <- net.JoinHostPort(ip.String(), strconv.Itoa(port)):
				case <-ctx.Done():
					break feed
				}
			}
		}
		close(work)
		workers.Wait()
		d.mu.Lock()
		job.Status = "completed"
		if ctx.Err() == context.DeadlineExceeded {
			job.Status = "timeout"
		} else if ctx.Err() != nil {
			job.Status = "cancelled"
		}
		d.mu.Unlock()
	}()
	return out, nil
}
func (d *discoveryRuntime) cancelJob(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	j, ok := d.jobs[id]
	if ok && j.Status == "running" {
		j.cancel()
	}
	return ok
}
func (d *discoveryRuntime) stop() {
	d.mu.Lock()
	d.stopped = true
	for _, j := range d.jobs {
		if j.Status == "running" {
			j.cancel()
		}
	}
	d.mu.Unlock()
	d.wg.Wait()
}
func (h *apiHandler) registerDiscovery(r chi.Router) {
	r.Post("/api/sites/discovery", func(w http.ResponseWriter, q *http.Request) {
		var spec discoverySpec
		if api.DecodeBody(q, &spec) != nil {
			api.Fail(w, 400, "请求格式错误")
			return
		}
		j, e := h.svc.discovery.start(spec)
		if e != nil {
			api.Fail(w, 400, e.Error())
			return
		}
		api.OK(w, j)
	})
	r.Get("/api/sites/discovery/{job}", func(w http.ResponseWriter, q *http.Request) {
		j, ok := h.svc.discovery.snapshot(chi.URLParam(q, "job"))
		if !ok {
			api.Fail(w, 404, "发现任务不存在或已过期")
			return
		}
		api.OK(w, j)
	})
	r.Post("/api/sites/discovery/{job}/cancel", func(w http.ResponseWriter, q *http.Request) {
		if !h.svc.discovery.cancelJob(chi.URLParam(q, "job")) {
			api.Fail(w, 404, "发现任务不存在或已过期")
			return
		}
		api.OK(w, nil)
	})
}
