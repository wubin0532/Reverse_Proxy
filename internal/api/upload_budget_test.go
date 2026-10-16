package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type budgetRecorder struct {
	*httptest.ResponseRecorder
	read, write time.Time
}

func (w *budgetRecorder) SetReadDeadline(at time.Time) error  { w.read = at; return nil }
func (w *budgetRecorder) SetWriteDeadline(at time.Time) error { w.write = at; return nil }
func TestUploadBudgetAllowsResponseAfterSlowUpload(t *testing.T) {
	s, _ := testServer(t)
	for _, path := range []string{"/api/system/update/inspect", "/api/system/backup/import", "/api/login"} {
		w := &budgetRecorder{ResponseRecorder: httptest.NewRecorder()}
		start := time.Now()
		s.requestBudget(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if path == "/api/login" {
			if !w.write.IsZero() || w.read.Sub(start) > time.Minute {
				t.Fatal("ordinary request budget changed")
			}
		} else if w.read.Sub(start) < 10*time.Minute || w.write.Sub(w.read) != 2*time.Minute {
			t.Fatalf("large request deadlines read=%v write=%v", w.read, w.write)
		}
	}
}
