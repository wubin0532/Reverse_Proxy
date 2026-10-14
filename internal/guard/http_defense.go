package guard

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/config"
	coreruleset "github.com/corazawaf/coraza-coreruleset/v4"
	coraza "github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/types"
)

var sharedWAF struct {
	once   sync.Once
	engine coraza.WAF
	err    error
}
var wafTransactions = make(chan struct{}, 8)

func prepareWAF() (coraza.WAF, error) {
	sharedWAF.once.Do(func() {
		sharedWAF.engine, sharedWAF.err = coraza.NewWAF(coraza.NewWAFConfig().WithRootFS(coreruleset.FS).WithErrorCallback(func(types.MatchedRule) {}).WithDirectives(`
SecRuleEngine On
SecRequestBodyAccess On
SecRequestBodyLimit 1048576
SecRequestBodyNoFilesLimit 1048576
SecRequestBodyInMemoryLimit 1048576
SecRequestBodyLimitAction Reject
SecResponseBodyAccess Off
SecAuditEngine Off
SecDebugLogLevel 0
SecRule REQUEST_HEADERS:Content-Type "@rx (?i)^application/json" "id:200001,phase:1,t:none,pass,nolog,ctl:requestBodyProcessor=JSON"
SecRule REQUEST_HEADERS:Content-Type "@rx (?i)^(application|text)/xml" "id:200002,phase:1,t:none,pass,nolog,ctl:requestBodyProcessor=XML"
Include @crs-setup.conf.example
Include @owasp_crs/REQUEST-*.conf
`))
	})
	return sharedWAF.engine, sharedWAF.err
}

type ccCounter struct {
	at                 time.Time
	requests, notFound int
	settings           config.CCConfig
}
type ccRuntime struct {
	mu     sync.Mutex
	counts map[string]ccCounter
}

func (e *Engine) httpPolicies(ids []string) []compiledPolicy {
	if e == nil {
		return nil
	}
	snapshot := e.snapshot.Load()
	out := []compiledPolicy{}
	seen := map[string]bool{}
	for _, p := range snapshot.globals {
		if !seen[p.conf.ID] {
			seen[p.conf.ID] = true
			out = append(out, p)
		}
	}
	for _, id := range ids {
		if p, ok := snapshot.policies[id]; ok && p.conf.Enabled && !seen[id] {
			seen[id] = true
			out = append(out, p)
		}
	}
	return out
}
func (e *Engine) checkCC(p compiledPolicy, ip, entity string, at time.Time) bool {
	c := p.conf.CC
	if c == nil || !c.Enabled || c.MaxRequests == 0 {
		return true
	}
	e.cc.mu.Lock()
	if e.cc.counts == nil {
		e.cc.counts = map[string]ccCounter{}
	}
	for key, v := range e.cc.counts {
		if at.Sub(v.at) >= time.Duration(v.settings.WindowSeconds)*time.Second {
			delete(e.cc.counts, key)
		}
	}
	key := p.conf.ID + "|" + ip
	counter, ok := e.cc.counts[key]
	if !ok && len(e.cc.counts) >= 4096 {
		e.cc.mu.Unlock()
		return e.deny(ip, "", "web", entity, p.conf.ID, "CC计数容量达到上限")
	}
	if !ok || counter.settings != *c {
		counter = ccCounter{at: at, settings: *c}
	}
	counter.requests++
	e.cc.counts[key] = counter
	allowed := counter.requests <= c.MaxRequests
	e.cc.mu.Unlock()
	if allowed {
		return true
	}
	if c.BanSeconds > 0 {
		_ = e.AddBan(Ban{IP: ip, Reason: "CC请求频率超限", ExpiresAt: at.Add(time.Duration(c.BanSeconds) * time.Second)})
	}
	return e.deny(ip, "", "web", entity, p.conf.ID, "CC请求频率超限")
}
func (e *Engine) ObserveHTTPStatus(ip, entity string, status int, ids []string) {
	if e == nil {
		return
	}
	for _, p := range e.httpPolicies(ids) {
		c := p.conf.CC
		if c == nil || !c.Enabled || c.NotFoundThreshold == 0 || c.BanSeconds == 0 {
			continue
		}
		at := time.Now()
		key := p.conf.ID + "|" + ip
		e.cc.mu.Lock()
		if e.cc.counts == nil {
			e.cc.counts = map[string]ccCounter{}
		}
		counter, ok := e.cc.counts[key]
		if !ok && len(e.cc.counts) >= 4096 {
			e.cc.mu.Unlock()
			continue
		}
		if !ok || counter.settings != *c || at.Sub(counter.at) >= time.Duration(c.WindowSeconds)*time.Second {
			counter = ccCounter{at: at, settings: *c}
		}
		if status == 404 {
			counter.notFound++
		} else {
			counter.notFound = 0
		}
		ban := counter.notFound >= c.NotFoundThreshold
		e.cc.counts[key] = counter
		e.cc.mu.Unlock()
		if ban {
			_ = e.AddBan(Ban{IP: ip, Reason: "连续404超限", ExpiresAt: at.Add(time.Duration(c.BanSeconds) * time.Second)})
		}
	}
}
func (e *Engine) wafEvent(ip, entity, policy, reason string, observe bool) {
	if !observe {
		e.deny(ip, "", "web", entity, policy, reason)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextID++
	event := Event{e.nextID, time.Now().UTC(), "web", entity, ip, policy, reason, "observe"}
	if len(e.events) == MaxEvents {
		copy(e.events, e.events[1:])
		e.events[len(e.events)-1] = event
	} else {
		e.events = append(e.events, event)
	}
}

type restoredBody struct {
	io.Reader
	original io.ReadCloser
}

func (b restoredBody) Close() error { return b.original.Close() }
func (e *Engine) InspectHTTP(w http.ResponseWriter, r *http.Request, ip, entity string, ids []string) bool {
	policies := e.httpPolicies(ids)
	var chosen *compiledPolicy
	bodyLimitKiB := 1024
	for i := range policies {
		p := &policies[i]
		if !e.checkCC(*p, ip, entity, time.Now()) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "429 Request frequency exceeded", 429)
			return false
		}
		if p.conf.WAF != nil && p.conf.WAF.Enabled {
			bodyLimitKiB = min(bodyLimitKiB, p.conf.WAF.BodyLimitKiB)
			if chosen == nil || chosen.conf.WAF.DetectionOnly && !p.conf.WAF.DetectionOnly {
				chosen = p
			}
		}
	}
	if chosen == nil {
		return true
	}
	settings := chosen.conf.WAF
	observe := settings.DetectionOnly
	select {
	case wafTransactions <- struct{}{}:
		defer func() { <-wafTransactions }()
	default:
		http.Error(w, "503 WAF capacity reached", 503)
		return false
	}
	waf, err := prepareWAF()
	if err != nil {
		http.Error(w, "503 WAF unavailable", 503)
		return false
	}
	tx := waf.NewTransaction()
	defer tx.Close()
	defer tx.ProcessLogging()
	tx.ProcessConnection(ip, 0, "0.0.0.0", 0)
	tx.SetServerName(r.Host)
	tx.ProcessURI(r.URL.RequestURI(), r.Method, r.Proto)
	tx.AddRequestHeader("Host", r.Host)
	for key, values := range r.Header {
		for _, value := range values {
			tx.AddRequestHeader(key, value)
		}
	}
	for key, values := range r.URL.Query() {
		for _, value := range values {
			tx.AddGetRequestArgument(key, value)
		}
	}
	interrupted := tx.ProcessRequestHeaders() != nil
	limit := int64(bodyLimitKiB) << 10
	if !interrupted && r.Body != nil && r.Body != http.NoBody {
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(10 * time.Second))
		original := r.Body
		body, readErr := io.ReadAll(io.LimitReader(original, limit+1))
		_ = controller.SetReadDeadline(time.Time{})
		r.Body = restoredBody{Reader: io.MultiReader(bytes.NewReader(body), original), original: original}
		if readErr != nil {
			e.wafEvent(ip, entity, chosen.conf.ID, "WAF请求体读取失败", observe)
			http.Error(w, "400 Request body could not be read", 400)
			return false
		}
		if int64(len(body)) > limit {
			e.wafEvent(ip, entity, chosen.conf.ID, "WAF请求体超过检测上限", observe)
			if !observe {
				http.Error(w, "413 Request body exceeds WAF limit", 413)
				return false
			}
			body = body[:limit]
		}
		_, _, err = tx.WriteRequestBody(body)
	}
	if !interrupted && err == nil {
		_, err = tx.ProcessRequestBody()
	}
	interrupted = tx.IsInterrupted()
	matchedID := 0
	for _, matched := range tx.MatchedRules() {
		id := matched.Rule().ID()
		if id >= 910000 && id < 950000 {
			for _, tag := range matched.Rule().Tags() {
				if strings.HasPrefix(tag, "attack-") {
					matchedID = id
					break
				}
			}
		}
		if matchedID != 0 {
			break
		}
	}
	if interrupted || matchedID != 0 || err != nil {
		reason := fmt.Sprintf("WAF规则命中（%d）", matchedID)
		if err != nil {
			reason = "WAF请求解析失败"
		}
		e.wafEvent(ip, entity, chosen.conf.ID, reason, observe || (!interrupted && err == nil))
		if !observe && (interrupted || err != nil) {
			status := 403
			if err != nil {
				status = 400
			}
			http.Error(w, "Request rejected by WAF", status)
			return false
		}
	}
	return true
}
