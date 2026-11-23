package scheduler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/INKCR0W/jm-automation/internal/api"
	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/internal/config"
	"github.com/robfig/cron/v3"
)

func TestStopCancelsHTTP2Redial(t *testing.T) {
	const childEnv = "JM_TEST_HTTP2_REDIAL"
	if os.Getenv(childEnv) != "1" {
		// x509.SetFallbackRoots can only be called once per process
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, executable, "-test.run=^TestStopCancelsHTTP2Redial$", "-test.v")
		command.Env = append(os.Environ(), childEnv+"=1", "GODEBUG=x509usefallbackroots=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("HTTP/2 regression subprocess: %v\n%s", err, output)
		}
		t.Logf("%s", output)
		return
	}

	var stall atomic.Bool
	var reportRedial sync.Once
	redialStarted := make(chan struct{})
	releaseHandshake := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-HTTP-Protocol", r.Proto)
		w.WriteHeader(http.StatusNoContent)
	}))
	server.EnableHTTP2 = true
	server.Config.ErrorLog = log.New(t.Output(), "", 0)
	server.TLS = &tls.Config{
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			if stall.Load() {
				reportRedial.Do(func() { close(redialStarted) })
				<-releaseHandshake
			}
			return nil, nil
		},
	}
	server.StartTLS()
	defer server.Close()

	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	x509.SetFallbackRoots(roots)

	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := &Scheduler{
		lifetime:  ctx,
		cron:      cron.New(),
		clientMap: make(map[string]*client.Client),
		baseURLs:  []string{server.URL},
		config: &config.Config{
			Server:    config.ServerConfig{BaseURL: server.URL, Timeout: 30},
			Scheduler: config.SchedulerConfig{Cron: "@every 1s"},
		},
	}
	defer func() {
		cancel()
		close(releaseHandshake)
		s.Stop()
	}()

	c, err := s.getOrCreateClient("loopback-test")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(ctx, "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if protocol := resp.Headers.Get("X-HTTP-Protocol"); protocol != "HTTP/2.0" {
		t.Fatalf("protocol = %q, want HTTP/2.0", protocol)
	}
	stall.Store(true)
	server.CloseClientConnections()

	result := make(chan error, 1)
	s.resolveBaseURLs = func(ctx context.Context, _ string) api.Resolution {
		_, err := c.Get(ctx, "/", nil)
		result <- err
		return api.Resolution{}
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-redialStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP/2 redial did not reach the TLS handshake")
	}

	start := time.Now()
	cancel()
	stopped := make(chan struct{})
	go func() {
		s.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not stop while the peer stalled the TLS handshake")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("request error = %v, want context.Canceled", err)
	}
	t.Logf("scheduler drained in %s after cancellation", time.Since(start))
}
