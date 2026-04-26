package process

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/zzci/supervisord/internal/config"
)

// healthchecker reports whether a single attempt against the program succeeds.
// Implementations must respect the supplied context for cancellation and the
// per-attempt timeout encoded by the caller.
type healthchecker interface {
	Check(ctx context.Context) error
}

// buildHealthcheckers reads per-program healthcheck fields and returns the list
// of configured checkers. Returns nil when no healthcheck field is set; the
// caller should skip starting the healthcheck loop in that case.
func buildHealthcheckers(cfg *config.Entry) []healthchecker {
	var checkers []healthchecker
	if url := cfg.GetString("healthcheck_http", ""); url != "" {
		checkers = append(checkers, httpHealthcheck{url: url})
	}
	if addr := cfg.GetString("healthcheck_tcp", ""); addr != "" {
		checkers = append(checkers, tcpHealthcheck{address: addr})
	}
	if path := cfg.GetString("healthcheck_file", ""); path != "" {
		checkers = append(checkers, fileHealthcheck{path: path})
	}
	if cmd := cfg.GetString("healthcheck_command", ""); cmd != "" {
		checkers = append(checkers, commandHealthcheck{commandLine: cmd})
	}
	return checkers
}

type httpHealthcheck struct {
	url string
}

func (h httpHealthcheck) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http %s returned %d", h.url, resp.StatusCode)
	}
	return nil
}

type tcpHealthcheck struct {
	address string
}

func (t tcpHealthcheck) Check(ctx context.Context) error {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", t.address)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

type fileHealthcheck struct {
	path string
}

func (f fileHealthcheck) Check(_ context.Context) error {
	if _, err := os.Stat(f.path); err != nil {
		return err
	}
	return nil
}

type commandHealthcheck struct {
	commandLine string
}

func (c commandHealthcheck) Check(ctx context.Context) error {
	// #nosec G204 -- healthcheck command is from supervisor.conf, by design
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", c.commandLine)
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

// runHealthcheckLoop drives the registered checkers on the configured cadence
// and flips p.healthy to true once `retries` consecutive attempts succeed. The
// loop terminates when ctx is canceled (program no longer Running). The flag
// is reset to false whenever the loop starts.
func runHealthcheckLoop(ctx context.Context, p *Process, checkers []healthchecker) {
	cfg := p.config
	timeout := time.Duration(cfg.GetInt("healthcheck_timeout", 5)) * time.Second
	interval := time.Duration(cfg.GetInt("healthcheck_interval", 2)) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	retries := cfg.GetInt("healthcheck_retries", 3)
	if retries <= 0 {
		retries = 1
	}

	p.setHealthy(false)
	consecutive := 0
	for {
		if ok := runOneAttempt(ctx, checkers, timeout); ok {
			consecutive++
			if consecutive >= retries {
				p.setHealthy(true)
				// Stay alive: keep checking so we can flip back on failure later.
			}
		} else {
			if consecutive > 0 || p.IsHealthy() {
				consecutive = 0
				p.setHealthy(false)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// runOneAttempt invokes each checker once with the per-attempt timeout. All
// must succeed for the attempt to count as a success.
func runOneAttempt(parent context.Context, checkers []healthchecker, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	for _, c := range checkers {
		ctx, cancel := context.WithTimeout(parent, timeout)
		err := c.Check(ctx)
		cancel()
		if err != nil {
			return false
		}
	}
	return true
}
