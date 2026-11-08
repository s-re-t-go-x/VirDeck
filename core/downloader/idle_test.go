package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDownloadIdleTimeoutRetainsPartialAndReportsCause(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("begin"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	c := New(t.TempDir(), "")
	c.idleTimeout = 50 * time.Millisecond
	_, err := c.Download(context.Background(), "idle", "test.zip", server.URL, "", nil)
	if err == nil || !strings.Contains(err.Error(), "无数据") {
		t.Fatalf("missing idle timeout: %v", err)
	}
}
