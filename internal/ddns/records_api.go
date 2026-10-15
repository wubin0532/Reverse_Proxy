package ddns

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"github.com/go-chi/chi/v5"
)

var errTaskConflict = errors.New("任务已变更，请刷新后重试")
var errRecordNotFound = errors.New("记录不存在")

func (h *handler) taskMutationError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, errTaskConflict):
		api.Fail(w, 409, err.Error())
	case errors.Is(err, errTaskNotFound), errors.Is(err, errRecordNotFound):
		api.Fail(w, 404, err.Error())
	case errors.Is(err, errProviderNotFound):
		api.Fail(w, 400, "引用的服务商凭据不存在")
	case strings.HasPrefix(err.Error(), "validation:"):
		parts := strings.SplitN(err.Error(), ":", 3)
		code, _ := strconv.Atoi(parts[1])
		api.Fail(w, code, parts[2])
	default:
		api.Fail(w, 500, "保存配置失败")
	}
	return true
}

func (h *handler) taskHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.cfg.RLock()
	found := false
	for _, task := range h.cfg.DDNS {
		if task.ID == id {
			found = true
			break
		}
	}
	h.cfg.RUnlock()
	if !found {
		api.Fail(w, 404, "任务不存在")
		return
	}
	api.OK(w, map[string]interface{}{"entries": h.w.History(id), "capacity": 100, "persistent": false})
}

func (h *handler) createRecord(w http.ResponseWriter, r *http.Request) {
	h.mutateRecord(w, r, "create")
}
func (h *handler) updateRecord(w http.ResponseWriter, r *http.Request) {
	h.mutateRecord(w, r, "update")
}
func (h *handler) deleteRecord(w http.ResponseWriter, r *http.Request) {
	h.mutateRecord(w, r, "delete")
}
func (h *handler) toggleRecord(w http.ResponseWriter, r *http.Request) {
	h.mutateRecord(w, r, "toggle")
}

func (h *handler) mutateRecord(w http.ResponseWriter, r *http.Request, action string) {
	var body struct {
		Revision string            `json:"revision"`
		Record   config.DDNSRecord `json:"record"`
	}
	if err := api.DecodeBody(r, &body); err != nil {
		api.FailDecode(w, err, "请求格式错误")
		return
	}
	id, recordID := chi.URLParam(r, "id"), chi.URLParam(r, "recordId")
	var updated config.DDNSTask
	err := h.cfg.Update(func(cfg *config.Config) error {
		for i, current := range cfg.DDNS {
			if current.ID != id {
				continue
			}
			if body.Revision == "" || body.Revision != taskRevision(current) {
				return errTaskConflict
			}
			current.Records = effectiveRecords(current)
			if action == "create" {
				body.Record.ID = ids.New()
				current.Records = append(current.Records, body.Record)
			} else {
				found := false
				for j, record := range current.Records {
					if record.ID != recordID {
						continue
					}
					found = true
					switch action {
					case "update":
						body.Record.ID = recordID
						current.Records[j] = body.Record
					case "delete":
						if len(current.Records) == 1 {
							return fmt.Errorf("validation:400:任务至少保留一条记录，可停用最后一条记录")
						}
						current.Records = append(current.Records[:j], current.Records[j+1:]...)
					case "toggle":
						current.Records[j].Enabled = !record.Enabled
					}
					break
				}
				if !found {
					return errRecordNotFound
				}
			}
			if code, msg := validateTaskShape(&current); code != 0 {
				return fmt.Errorf("validation:%d:%s", code, msg)
			}
			if !providerExistsLocked(cfg, current.ProviderID) {
				return errProviderNotFound
			}
			cfg.DDNS[i] = current
			updated = current
			return nil
		}
		return errTaskNotFound
	})
	if h.taskMutationError(w, err) {
		return
	}
	h.w.Reload()
	api.OK(w, h.taskView(updated))
}
