package webproxy

import (
	"andey-proxy/internal/config"
	"andey-proxy/internal/guard"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

var errRevisionConflict = errors.New("配置已被其他编辑修改，请重新加载后再保存")

type ruleMutation struct {
	expected *uint64
	ruleID   string
	health   *HealthCheckConf
}
type ruleInput struct {
	config.SubRule
	ExpectedRevision *uint64          `json:"expectedRevision"`
	HealthCheck      *HealthCheckConf `json:"healthCheck"`
}
type siteInput struct {
	config.Site
	ExpectedRevision *uint64 `json:"expectedRevision"`
}

func headerRevision(r *http.Request) (*uint64, error) {
	v := r.Header.Get("If-Match")
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseUint(strings.Trim(v, "\""), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("If-Match版本无效")
	}
	return &n, nil
}
func checkRevision(expected *uint64, current uint64) error {
	if expected == nil {
		if current == 0 {
			return nil
		}
		return errRevisionConflict
	}
	if *expected != current {
		return errRevisionConflict
	}
	return nil
}
func bumpSite(next *config.Site, previous config.Site) {
	next.Revision = previous.Revision + 1
	old := map[string]config.SubRule{}
	for _, r := range previous.Rules {
		old[r.ID] = r
	}
	for i := range next.Rules {
		r := &next.Rules[i]
		p, ok := old[r.ID]
		r.Revision = p.Revision
		if !ok || !reflect.DeepEqual(*r, p) {
			r.Revision = p.Revision + 1
		}
	}
}
func validateReferences(c *config.Config, site config.Site) error {
	if site.CertID != "" {
		found := false
		for _, cert := range c.Certs {
			if cert.ID == site.CertID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("引用的证书不存在")
		}
	}
	if err := guard.ValidateReferenceLocked(c, site.PolicyID); err != nil {
		return err
	}
	for _, r := range site.Rules {
		if err := guard.ValidateReferenceLocked(c, r.PolicyID); err != nil {
			return err
		}
	}
	return nil
}
func validateWorkspace(site *config.Site) error {
	if site.DefaultResponse != nil {
		d := site.DefaultResponse
		if d.StatusCode < 200 || d.StatusCode > 599 || d.StatusCode >= 300 && d.StatusCode < 400 || len(d.Body) > 65536 {
			return fmt.Errorf("默认响应须为非跳转的200到599状态，正文最多64KiB")
		}
		if (d.StatusCode == 204 || d.StatusCode == 304) && d.Body != "" {
			return fmt.Errorf("该状态码不能包含正文")
		}
	}
	for i := range site.Rules {
		r := &site.Rules[i]
		r.Group = strings.TrimSpace(r.Group)
		if len(r.Group) > 128 {
			return fmt.Errorf("规则分组最多128字节")
		}
	}
	return nil
}
