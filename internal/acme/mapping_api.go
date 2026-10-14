package acme

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

var errMappingConflict = errors.New("映射已被其他编辑修改，草稿已保留")
var errMappingInvalid = errors.New("映射配置无效")

func mappingRevision(mappings []config.CertificateMapping) string {
	b, _ := json.Marshal(mappings)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func mappingPaths(dir string, c config.CertConf) []string {
	out := []string{}
	for _, m := range c.Mappings {
		out = append(out, filepath.Join(dir, "exports", filepath.Base(c.ID), m.Name, "current"))
	}
	return out
}
func (h *handler) updateMappings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Revision string                      `json:"revision"`
		Mappings []config.CertificateMapping `json:"mappings"`
	}
	if api.DecodeBody(r, &body) != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	err := h.cfg.Update(func(c *config.Config) error {
		for i := range c.Certs {
			if c.Certs[i].ID != id {
				continue
			}
			cert := c.Certs[i]
			if body.Revision == "" || body.Revision != mappingRevision(cert.Mappings) {
				return errMappingConflict
			}
			cert.Mappings = append([]config.CertificateMapping(nil), body.Mappings...)
			if err := cert.ValidateAdvanced(); err != nil {
				return fmt.Errorf("%w: %s", errMappingInvalid, err)
			}
			c.Certs[i] = cert
			return nil
		}
		return errCertNotFound
	})
	if err != nil {
		code := 500
		if errors.Is(err, errCertNotFound) {
			code = 404
		}
		if errors.Is(err, errMappingConflict) {
			code = 409
		}
		if errors.Is(err, errMappingInvalid) {
			code = 400
		}
		api.Fail(w, code, err.Error())
		return
	}
	c, _ := h.m.findCert(id)
	api.OK(w, h.viewOf(c, time.Now()))
}
