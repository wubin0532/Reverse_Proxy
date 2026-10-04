package webproxy

import "andey-proxy/internal/config"

// The store reads Config directly so backup restore never leaves a stale cache.
type healthStore struct{ cfg *config.Config }

func newHealthStore(cfg *config.Config) *healthStore { return &healthStore{cfg: cfg} }

func (st *healthStore) confFor(ruleID string) HealthCheckConf {
	st.cfg.RLock()
	defer st.cfg.RUnlock()
	return HealthCheckConf(st.cfg.RuleHealthChecks[ruleID]).withDefaults()
}

func (st *healthStore) set(ruleID string, conf HealthCheckConf) error {
	if err := conf.validate(); err != nil {
		return err
	}
	return st.cfg.Update(func(c *config.Config) error {
		if c.RuleHealthChecks == nil {
			c.RuleHealthChecks = make(map[string]config.HealthCheckConf)
		}
		c.RuleHealthChecks[ruleID] = config.HealthCheckConf(conf)
		return nil
	})
}

func (st *healthStore) delete(ruleID string) error {
	return st.cfg.Update(func(c *config.Config) error { delete(c.RuleHealthChecks, ruleID); return nil })
}

func (st *healthStore) prune(keep map[string]bool) error {
	return st.cfg.Update(func(c *config.Config) error {
		for id := range c.RuleHealthChecks {
			if !keep[id] {
				delete(c.RuleHealthChecks, id)
			}
		}
		return nil
	})
}

// Existing handlers must apply restored settings even before the next request.
func (s *Service) reloadRuleHealth() {
	s.cfg.RLock()
	type entry struct {
		siteID string
		rule   config.SubRule
		conf   HealthCheckConf
	}
	var entries []entry
	for _, site := range s.cfg.Sites {
		for _, rule := range site.Rules {
			entries = append(entries, entry{site.ID, rule, HealthCheckConf(s.cfg.RuleHealthChecks[rule.ID]).withDefaults()})
		}
	}
	s.cfg.RUnlock()
	for _, item := range entries {
		s.applyRuleHealth(item.siteID, item.rule, item.conf)
	}
}
