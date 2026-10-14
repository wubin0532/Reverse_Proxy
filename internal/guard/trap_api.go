package guard

import (
	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func registerTrapRoutes(r chi.Router, e *Engine) {
	r.Get("/api/protection/trap", func(w http.ResponseWriter, _ *http.Request) { api.OK(w, e.Trap.Status()) })
	r.Put("/api/protection/trap", func(w http.ResponseWriter, req *http.Request) {
		e.mutation.Lock()
		defer e.mutation.Unlock()
		var conf config.TCPTrap
		if api.DecodeBody(req, &conf) != nil {
			api.Fail(w, 400, "请求格式错误")
			return
		}
		if err := e.Trap.Apply(conf, true); err != nil {
			api.Fail(w, 400, err.Error())
			return
		}
		api.OK(w, e.Trap.Status())
	})
}
